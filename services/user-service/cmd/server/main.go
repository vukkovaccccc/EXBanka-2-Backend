// user-service entrypoint.
//
// Starts two servers concurrently:
//   - gRPC server          on 0.0.0.0:50051  (standard net/grpc)
//   - gRPC-Gateway HTTP    on 0.0.0.0:8080   (grpc-gateway/v2 runtime.ServeMux)
//
// The HTTP gateway is a reverse-proxy that translates REST calls into gRPC
// calls against the local gRPC server at localhost:50051. All routing is
// derived from the `google.api.http` annotations in proto/user/user.proto.
//
// All configuration is loaded from environment variables via internal/config.
package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "banka-backend/proto/user"
	auth "banka-backend/shared/auth"
	"banka-backend/services/user-service/internal/config"
	dbsqlc "banka-backend/services/user-service/internal/database/sqlc"
	userhandler "banka-backend/services/user-service/internal/handler"
	userservice "banka-backend/services/user-service/internal/service"
	"banka-backend/services/user-service/internal/transport"
	"banka-backend/services/user-service/internal/utils"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" driver for database/sql
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

	// ── 2. Database ───────────────────────────────────────────────────────────
	// pgx/v5/stdlib is already in go.mod (pulled by GORM); it registers itself
	// as the "pgx" driver for database/sql — no extra dependency needed.
	// cfg.DSN() returns a libpq key-value string accepted by pgx/v5/stdlib.
	sqlDB, err := sql.Open("pgx", cfg.DSN())
	if err != nil {
		log.Fatalf("[db] open: %v", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("[db] ping: %v", err)
	}
	log.Println("[db] connected to PostgreSQL")

	querier := dbsqlc.New(sqlDB)

	// ── 3. gRPC server ───────────────────────────────────────────────────────
	// transport.NewGRPCServer already registers:
	//   • grpc_health_v1 (for grpc-health-probe / k8s)
	//   • server reflection (for grpcurl / Postman discovery)
	//   • logging unary interceptor
	//
	// AuthInterceptor is prepended so it runs before logging and can reject
	// unauthenticated requests early. The secret is read from JWT_ACCESS_SECRET
	// (falls back to "change-me-access-secret" in config.Load for local dev).
	authInterceptor := auth.NewAuthInterceptor(cfg.JWTAccessSecret, []string{
		pb.UserService_HealthCheck_FullMethodName,
		pb.UserService_Login_FullMethodName,
		pb.UserService_SetPassword_FullMethodName,     // activation token is the credential, no access token
		pb.UserService_ActivateAccount_FullMethodName,
		pb.UserService_RefreshToken_FullMethodName,    // carries a refresh token, not an access token
		pb.UserService_ForgotPassword_FullMethodName,  // unauthenticated — only an email is provided
		pb.UserService_ResetPassword_FullMethodName,   // reset token is the credential, no access token
	})
	grpcSrv := transport.NewGRPCServer(cfg.GRPCAddr, authInterceptor.Unary())

	// ── bank-service client (optional — nil disables actuary sync) ──────────────
	var bankClient userhandler.BankActuaryClient
	if cfg.BankServiceAddr != "" {
		bankClient = transport.NewBankServiceClient(cfg.BankServiceAddr)
		log.Printf("[main] bank-service klijent konfigurisan na %s", cfg.BankServiceAddr)
	} else {
		log.Printf("[main] BANK_SERVICE_ADDR nije postavljen — aktuar sinhronizacija neće biti aktivna")
	}

	handler := userhandler.NewUserHandler(querier, sqlDB, cfg.JWTAccessSecret, cfg.JWTRefreshSecret, cfg.JWTActivationSecret, utils.NewAMQPPublisher(cfg.RabbitMQURL), utils.NewAMQPUserCreatedPublisher(cfg.RabbitMQURL), userservice.NewClientService(querier), bankClient)
	pb.RegisterUserServiceServer(grpcSrv.Server(), handler)

	// ── 4. gRPC-Gateway: dial the local gRPC server ──────────────────────────
	// A background context is used for the gateway connection; it is cancelled
	// during graceful shutdown via the defer below.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Dial the local gRPC server with insecure credentials (no TLS within the
	// same pod / process). grpc.DialContext is used per project spec.
	//
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

	// Wire the UserService client into the gateway mux.
	// RegisterUserServiceHandlerClient is generated by protoc-gen-grpc-gateway.
	mux := runtime.NewServeMux()
	if err := pb.RegisterUserServiceHandlerClient(
		ctx,
		mux,
		pb.NewUserServiceClient(conn),
	); err != nil {
		log.Fatalf("[gateway] register handler client: %v", err)
	}

	// Obmotaj gateway mux sa custom handlerom za /client/{id}/trade-permission
	permHandler := userhandler.NewClientPermissionHandler(sqlDB, cfg.JWTAccessSecret)

	gatewaySrv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      permHandler.WrapMux(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// ── 5. Start gRPC server ─────────────────────────────────────────────────
	go func() {
		if err := grpcSrv.Serve(); err != nil {
			log.Fatalf("[grpc] serve error: %v", err)
		}
	}()

	// ── 6. Start gRPC-Gateway HTTP server ────────────────────────────────────
	go func() {
		log.Printf("[gateway] HTTP listening on %s → gRPC %s", cfg.HTTPAddr, grpcLocalTarget)
		if err := gatewaySrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[gateway] ListenAndServe error: %v", err)
		}
	}()

	// ── 7. Graceful shutdown on SIGINT / SIGTERM ─────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("[main] shutdown signal received")

	// Give in-flight HTTP requests up to 10 s to complete.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := gatewaySrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[gateway] shutdown error: %v", err)
	}

	// GracefulStop waits for in-flight RPCs to complete.
	grpcSrv.Stop()

	log.Println("[main] clean shutdown complete")
}
