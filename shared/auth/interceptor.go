// Package auth provides gRPC server-side auth interceptors, shared across all microservices.
package auth

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// contextKey is an unexported type to prevent key collisions in context values.
type contextKey string

// claimsKey is the key under which *AccessClaims is stored in the context.
const claimsKey contextKey = "jwt_claims"

// AuthInterceptor verifies JWT access tokens on all protected RPCs.
type AuthInterceptor struct {
	accessSecret  string
	publicMethods map[string]struct{}
}

// NewAuthInterceptor constructs an AuthInterceptor using the given HMAC secret.
// publicMethods is a list of gRPC full-method paths (e.g. "/proto.UserService/Login")
// that bypass token validation — any other route requires a valid Bearer token.
func NewAuthInterceptor(accessSecret string, publicMethods []string) *AuthInterceptor {
	pm := make(map[string]struct{}, len(publicMethods))
	for _, m := range publicMethods {
		pm[m] = struct{}{}
	}
	return &AuthInterceptor{accessSecret: accessSecret, publicMethods: pm}
}

// Unary returns a grpc.UnaryServerInterceptor that:
//  1. Skips auth for whitelisted public methods.
//  2. Extracts the Bearer token from the incoming "authorization" metadata header.
//  3. Verifies the token and injects *AccessClaims into the context.
func (a *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if _, public := a.publicMethods[info.FullMethod]; public {
			return handler(ctx, req)
		}

		claims, err := a.extractClaims(ctx)
		if err != nil {
			return nil, err
		}

		return handler(context.WithValue(ctx, claimsKey, claims), req)
	}
}

// ClaimsFromContext retrieves the access claims injected by AuthInterceptor.
// Returns (nil, false) on public routes where no claims are present.
func ClaimsFromContext(ctx context.Context) (*AccessClaims, bool) {
	claims, ok := ctx.Value(claimsKey).(*AccessClaims)
	return claims, ok
}

// NewContextWithClaims injects pre-built JWT access claims into ctx using the
// same context key that the production Unary() interceptor uses.
//
// Intended exclusively for unit tests where you want to simulate an
// authenticated gRPC request without running the full interceptor chain.
func NewContextWithClaims(ctx context.Context, claims *AccessClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

// extractClaims reads and validates the Bearer token from incoming gRPC metadata.
// gRPC-Gateway forwards the HTTP Authorization header as lowercase "authorization".
func (a *AuthInterceptor) extractClaims(ctx context.Context) (*AccessClaims, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing metadata")
	}

	vals := md.Get("authorization")
	if len(vals) == 0 {
		return nil, status.Error(codes.Unauthenticated, "missing authorization header")
	}

	raw := vals[0]
	if !strings.HasPrefix(raw, "Bearer ") {
		return nil, status.Error(codes.Unauthenticated, "authorization header must use Bearer scheme")
	}
	tokenStr := strings.TrimPrefix(raw, "Bearer ")

	claims, err := VerifyToken(tokenStr, a.accessSecret)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}
	return claims, nil
}
