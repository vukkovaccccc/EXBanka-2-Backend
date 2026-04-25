package handler_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth "banka-backend/shared/auth"

	"banka-backend/services/user-service/internal/handler"
	sqlmock "github.com/DATA-DOG/go-sqlmock"
)

const testJWTSecret = "test-jwt-secret"

func newEmployeeToken(t *testing.T) string {
	t.Helper()
	tok, _, err := auth.GenerateTokens("1", "emp@bank.rs", "EMPLOYEE", nil, testJWTSecret, "ref-secret")
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	return tok
}

func TestNewClientPermissionHandler_NotNil(t *testing.T) {
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	if h == nil {
		t.Error("expected non-nil handler")
	}
}

func TestWrapMux_NonTradePerm_PassesThrough(t *testing.T) {
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := h.WrapMux(next)

	req := httptest.NewRequest(http.MethodGet, "/client/1/profile", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if !called {
		t.Error("expected next handler to be called for non-trade-permission path")
	}
}

func TestHandleTradePermission_NoToken_Unauthorized(t *testing.T) {
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestHandleTradePermission_InvalidToken_Forbidden(t *testing.T) {
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for invalid token, got %d", rec.Code)
	}
}

func TestHandleTradePermission_ClientToken_Forbidden(t *testing.T) {
	// CLIENT users are not allowed (only EMPLOYEE/ADMIN)
	tok, _, err := auth.GenerateTokens("2", "client@bank.rs", "CLIENT", nil, testJWTSecret, "ref-secret")
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for CLIENT token, got %d", rec.Code)
	}
}

func TestHandleTradePermission_InvalidClientID_BadRequest(t *testing.T) {
	tok := newEmployeeToken(t)
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/client/abc/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric client ID, got %d", rec.Code)
	}
}

func TestHandleTradePermission_ZeroClientID_BadRequest(t *testing.T) {
	tok := newEmployeeToken(t)
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/client/0/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for client ID 0, got %d", rec.Code)
	}
}

func TestHandleTradePermission_UnsupportedMethod_MethodNotAllowed(t *testing.T) {
	tok := newEmployeeToken(t)
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodDelete, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for DELETE method, got %d", rec.Code)
	}
}

func TestHandleTradePermission_PatchInvalidJSON_BadRequest(t *testing.T) {
	tok := newEmployeeToken(t)
	h := handler.NewClientPermissionHandler(nil, testJWTSecret)
	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPatch, "/client/1/trade-permission",
		strings.NewReader(`not-json`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid JSON body, got %d", rec.Code)
	}
}

func newMockDB(t *testing.T) (*handler.ClientPermissionHandler, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return handler.NewClientPermissionHandler(db, testJWTSecret), mock
}

func TestGetTradePermission_HasPermission_OK(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestGetTradePermission_NoPermission_OK(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectQuery("SELECT COUNT").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestGetTradePermission_DBError_InternalServerError(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectQuery("SELECT COUNT").
		WillReturnError(errDBFail)

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodGet, "/client/1/trade-permission", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on DB error, got %d", rec.Code)
	}
}

var errDBFail = errors.New("db failure")

func TestSetTradePermission_Grant_OK(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectExec("INSERT INTO user_permissions").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPatch, "/client/1/trade-permission",
		strings.NewReader(`{"grant":true}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for grant, got %d", rec.Code)
	}
}

func TestSetTradePermission_Revoke_OK(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectExec("DELETE FROM user_permissions").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(1, 1))

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPatch, "/client/1/trade-permission",
		strings.NewReader(`{"grant":false}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for revoke, got %d", rec.Code)
	}
}

func TestSetTradePermission_Grant_DBError(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectExec("INSERT INTO user_permissions").
		WillReturnError(errDBFail)

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPatch, "/client/1/trade-permission",
		strings.NewReader(`{"grant":true}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on DB error, got %d", rec.Code)
	}
}

func TestSetTradePermission_Revoke_DBError(t *testing.T) {
	tok := newEmployeeToken(t)
	h, mock := newMockDB(t)

	mock.ExpectExec("DELETE FROM user_permissions").
		WillReturnError(errDBFail)

	wrapped := h.WrapMux(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	req := httptest.NewRequest(http.MethodPatch, "/client/1/trade-permission",
		strings.NewReader(`{"grant":false}`))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 on DB error, got %d", rec.Code)
	}
}
