// Package interceptor provides a gRPC auth interceptor for the user-service.
// It wraps shared/auth.AuthInterceptor with the pre-defined set of public
// UserService methods that bypass token validation.
package interceptor

import (
	"context"

	pb "banka-backend/proto/user"
	"banka-backend/services/user-service/internal/utils"
	auth "banka-backend/shared/auth"

	"google.golang.org/grpc"
)

// userServicePublicMethods lists gRPC full-method names that do not require a
// valid access token. All other UserService routes are protected.
var userServicePublicMethods = []string{
	pb.UserService_HealthCheck_FullMethodName,
	pb.UserService_Login_FullMethodName,
	pb.UserService_SetPassword_FullMethodName,
	pb.UserService_ActivateAccount_FullMethodName,
	pb.UserService_RefreshToken_FullMethodName,
	pb.UserService_ForgotPassword_FullMethodName,
	pb.UserService_ResetPassword_FullMethodName,
}

// AuthInterceptor wraps auth.AuthInterceptor with user-service public routes.
type AuthInterceptor struct {
	inner *auth.AuthInterceptor
}

// NewAuthInterceptor constructs an AuthInterceptor using the given HMAC secret.
// The set of public (unauthenticated) methods is fixed to the UserService proto.
func NewAuthInterceptor(accessSecret string) *AuthInterceptor {
	return &AuthInterceptor{
		inner: auth.NewAuthInterceptor(accessSecret, userServicePublicMethods),
	}
}

// Unary returns a grpc.UnaryServerInterceptor that validates Bearer tokens on
// all protected routes and injects *AccessClaims into the request context.
func (a *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return a.inner.Unary()
}

// ClaimsFromContext retrieves the access claims injected by the interceptor.
// Returns (nil, false) when the request was not authenticated (public route).
func ClaimsFromContext(ctx context.Context) (*utils.AccessClaims, bool) {
	return auth.ClaimsFromContext(ctx)
}
