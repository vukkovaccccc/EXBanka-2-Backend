// bank-service entrypoint.
//
// bank-service entrypoint.
//
// Starts two servers concurrently:
//   - gRPC server          on 0.0.0.0:50051  (standard net/grpc)
//   - gRPC-Gateway HTTP    on 0.0.0.0:8080   (grpc-gateway/v2 runtime.ServeMux)
//
// The HTTP gateway is a reverse-proxy that translates REST calls into gRPC
// calls against the local gRPC server at localhost:50051.
//
// All configuration is loaded from environment variables via internal/config.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "banka-backend/proto/banka"
	auth "banka-backend/shared/auth"
	"banka-backend/services/bank-service/internal/config"
	"banka-backend/services/bank-service/internal/handler"
	"banka-backend/services/bank-service/internal/repository"
	"banka-backend/services/bank-service/internal/service"
	"banka-backend/services/bank-service/internal/transport"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// grpcLocalTarget is the address the gRPC-Gateway uses to dial back to the
// local gRPC server. Always localhost — both servers live in the same process.
const grpcLocalTarget = "localhost:50051"

func main() {
	// ── 1. Config ────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[main] config error: %v", err)
	}

	// ── 2. Database (GORM + PostgreSQL) ──────────────────────────────────────
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("[db] open: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("[db] get underlying sql.DB: %v", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("[db] ping: %v", err)
	}
	log.Println("[db] connected to PostgreSQL")

	// ── 3. Wire-up slojeva ───────────────────────────────────────────────────
	currencyRepo := repository.NewCurrencyRepository(db)
	currencyService := service.NewCurrencyService(currencyRepo)

	delatnostRepo := repository.NewDelatnostRepository(db)
	delatnostService := service.NewDelatnostService(delatnostRepo)

	accountRepo := repository.NewAccountRepository(db)
	accountService := service.NewAccountService(accountRepo, currencyRepo)

	recipientRepo := repository.NewPaymentRecipientRepository(db)
	paymentRepo := repository.NewPaymentRepository(db)
	paymentService := service.NewPaymentService(recipientRepo, paymentRepo)

	bankHandler := handler.NewBankHandler(currencyService, delatnostService, accountService, paymentService)
	receiptHandler := handler.NewPaymentReceiptHandler(paymentService, cfg.JWTAccessSecret)

	// ── 4. Auth interceptor ──────────────────────────────────────────────────
	// Sve rute zahtevaju validan JWT access token osim gRPC health check-a.
	authInterceptor := auth.NewAuthInterceptor(cfg.JWTAccessSecret, []string{
		"/grpc.health.v1.Health/Check",
	})

	// ── 5. gRPC server ───────────────────────────────────────────────────────
	grpcSrv := transport.NewGRPCServer(cfg.GRPCAddr, authInterceptor.Unary())
	pb.RegisterBankaServiceServer(grpcSrv.Server(), bankHandler)

	// ── 6. gRPC-Gateway: dial the local gRPC server ──────────────────────────
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	//nolint:staticcheck // grpc.DialContext is deprecated upstream; tracked for
	// migration to grpc.NewClient in a follow-up task.
	conn, err := grpc.DialContext(
		ctx,
		grpcLocalTarget,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("[gateway] dial gRPC backend: %v", err)
	}
	defer conn.Close()

	gwMux := runtime.NewServeMux()
	if err := pb.RegisterBankaServiceHandlerClient(
		ctx,
		gwMux,
		pb.NewBankaServiceClient(conn),
	); err != nil {
		log.Fatalf("[gateway] register handler client: %v", err)
	}

	// Kombinovani HTTP mux: gRPC-Gateway + direktni HTTP handleri (PDF potvrde).
	httpMux := http.NewServeMux()
	httpMux.Handle("/bank/payments/", receiptHandler) // GET /bank/payments/{id}/receipt
	httpMux.Handle("/", gwMux)                         // sve ostalo → gRPC-Gateway

	gatewaySrv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      httpMux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── 7. Start gRPC server ─────────────────────────────────────────────────
	go func() {
		if err := grpcSrv.Serve(); err != nil {
			log.Fatalf("[grpc] serve error: %v", err)
		}
	}()

	// ── 8. Start gRPC-Gateway HTTP server ────────────────────────────────────
	go func() {
		log.Printf("[gateway] HTTP listening on %s → gRPC %s", cfg.HTTPAddr, grpcLocalTarget)
		if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[gateway] ListenAndServe error: %v", err)
		}
	}()

	// ── 9. Graceful shutdown on SIGINT / SIGTERM ──────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutdown signal received")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := gatewaySrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[gateway] shutdown error: %v", err)
	}

	grpcSrv.Stop()
	log.Println("[main] clean shutdown complete")
}
