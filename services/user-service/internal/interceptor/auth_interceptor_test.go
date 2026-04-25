package interceptor

import (
	"context"
	"testing"
)

func TestNewAuthInterceptor_NotNil(t *testing.T) {
	ai := NewAuthInterceptor("test-secret")
	if ai == nil {
		t.Error("expected non-nil AuthInterceptor")
	}
}

func TestAuthInterceptor_Unary_NotNil(t *testing.T) {
	ai := NewAuthInterceptor("test-secret")
	fn := ai.Unary()
	if fn == nil {
		t.Error("expected non-nil UnaryServerInterceptor")
	}
}

func TestClaimsFromContext_NotSet(t *testing.T) {
	claims, ok := ClaimsFromContext(context.Background())
	if ok {
		t.Error("expected ok=false when claims not set")
	}
	if claims != nil {
		t.Error("expected nil claims when not set")
	}
}
