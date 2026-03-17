// Package transport sets up the gRPC server and registers all services.
// Clean Architecture: interface / delivery layer (transport sub-layer).
package transport

import (
	"context"
	"log"
	"net"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// GRPCServer wraps a *grpc.Server with lifecycle helpers.
type GRPCServer struct {
	server *grpc.Server
	addr   string
}

// NewGRPCServer creates a new gRPC server with logging middleware and built-in
// health service. Additional services should be registered on the returned
// *grpc.Server before calling Serve.
//
// extraInterceptors are prepended before the logging interceptor so that auth
// runs first and can short-circuit unauthenticated requests.
func NewGRPCServer(addr string, extraInterceptors ...grpc.UnaryServerInterceptor) *GRPCServer {
	chain := append(extraInterceptors, loggingInterceptor)
	s := grpc.NewServer(
		grpc.ChainUnaryInterceptor(chain...),
	)

	// Built-in gRPC health protocol — used by Docker, k8s, and grpc-health-probe.
	healthSvc := health.NewServer()
	grpc_health_v1.RegisterHealthServer(s, healthSvc)
	healthSvc.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Enable server reflection so grpcurl / Postman can discover methods.
	reflection.Register(s)

	return &GRPCServer{server: s, addr: addr}
}

// Server returns the underlying *grpc.Server for service registration.
func (g *GRPCServer) Server() *grpc.Server { return g.server }

// Serve binds the listener and blocks until the server stops.
func (g *GRPCServer) Serve() error {
	lis, err := net.Listen("tcp", g.addr)
	if err != nil {
		return err
	}
	log.Printf("[grpc] listening on %s", g.addr)
	return g.server.Serve(lis)
}

// Stop performs a graceful shutdown.
func (g *GRPCServer) Stop() {
	log.Println("[grpc] shutting down")
	g.server.GracefulStop()
}

// ─── Middleware ───────────────────────────────────────────────────────────────

func loggingInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	log.Printf("[grpc] → %s", info.FullMethod)
	resp, err := handler(ctx, req)
	if err != nil {
		log.Printf("[grpc] ✗ %s: %v", info.FullMethod, err)
	}
	return resp, err
}
