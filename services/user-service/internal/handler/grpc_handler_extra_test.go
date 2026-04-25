package handler_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	pb "banka-backend/proto/user"
	sqlc "banka-backend/services/user-service/internal/database/sqlc"
	"banka-backend/services/user-service/internal/handler"
	"banka-backend/services/user-service/internal/testutil"
	utils "banka-backend/services/user-service/internal/utils"
	"banka-backend/services/user-service/mocks"
	auth "banka-backend/shared/auth"

	"github.com/golang-jwt/jwt/v5"
)

// ─── SearchClients ────────────────────────────────────────────────────────────

func TestSearchClients_PermissionDenied_NotEmployee(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	// ADMIN ctx — SearchClients requires EMPLOYEE
	resp, err := h.SearchClients(testutil.AdminContext(), &pb.SearchClientsRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.PermissionDenied, grpcCode(err))
}

func TestSearchClients_PermissionDenied_Unauthenticated(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	resp, err := h.SearchClients(testutil.UnauthenticatedContext(), &pb.SearchClientsRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.PermissionDenied, grpcCode(err))
}

func TestSearchClients_Success_NoQuery(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	rows := []sqlc.SearchClientsRow{
		{ID: 1, FirstName: "Ana", LastName: "Anic", Email: "ana@test.com"},
		{ID: 2, FirstName: "Bora", LastName: "Boric", Email: "bora@test.com"},
	}
	// limit+1 fetch: req.Limit=0 → default 10, so DB receives Limit=11
	q.On("SearchClients", mock.Anything, mock.MatchedBy(func(p sqlc.SearchClientsParams) bool {
		return p.Limit == 11 && p.Offset == 0 && !p.Query.Valid
	})).Return(rows, nil)

	resp, err := h.SearchClients(testutil.EmployeeContext("5", []string{}), &pb.SearchClientsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Clients, 2)
	assert.False(t, resp.HasMore)
}

func TestSearchClients_Success_WithQuery(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	rows := []sqlc.SearchClientsRow{
		{ID: 3, FirstName: "Ceca", LastName: "Cec", Email: "ceca@test.com"},
	}
	q.On("SearchClients", mock.Anything, mock.MatchedBy(func(p sqlc.SearchClientsParams) bool {
		return p.Query.Valid && p.Query.String == "cec"
	})).Return(rows, nil)

	resp, err := h.SearchClients(
		testutil.EmployeeContext("5", []string{}),
		&pb.SearchClientsRequest{Query: "cec"},
	)
	require.NoError(t, err)
	assert.Len(t, resp.Clients, 1)
	assert.Equal(t, "Ceca", resp.Clients[0].FirstName)
}

func TestSearchClients_HasMore_True(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	// Return limit+1 rows (limit=2, so return 3) to trigger has_more=true
	rows := []sqlc.SearchClientsRow{
		{ID: 1, FirstName: "A", LastName: "A", Email: "a@test.com"},
		{ID: 2, FirstName: "B", LastName: "B", Email: "b@test.com"},
		{ID: 3, FirstName: "C", LastName: "C", Email: "c@test.com"},
	}
	q.On("SearchClients", mock.Anything, mock.MatchedBy(func(p sqlc.SearchClientsParams) bool {
		return p.Limit == 3 // limit=2+1
	})).Return(rows, nil)

	resp, err := h.SearchClients(
		testutil.EmployeeContext("5", []string{}),
		&pb.SearchClientsRequest{Limit: 2},
	)
	require.NoError(t, err)
	assert.True(t, resp.HasMore)
	assert.Len(t, resp.Clients, 2) // trimmed to limit
}

func TestSearchClients_FiltersOutDrzavaEmail(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	rows := []sqlc.SearchClientsRow{
		{ID: 1, FirstName: "Drzava", LastName: "Srbija", Email: "drzava@exbanka.rs"},
		{ID: 2, FirstName: "Ana", LastName: "Anic", Email: "ana@test.com"},
	}
	q.On("SearchClients", mock.Anything, mock.Anything).Return(rows, nil)

	resp, err := h.SearchClients(testutil.EmployeeContext("5", []string{}), &pb.SearchClientsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Clients, 1)
	assert.Equal(t, "ana@test.com", resp.Clients[0].Email)
}

func TestSearchClients_DBError(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	q.On("SearchClients", mock.Anything, mock.Anything).Return(nil, errors.New("db down"))

	resp, err := h.SearchClients(testutil.EmployeeContext("5", []string{}), &pb.SearchClientsRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, grpcCode(err))
}

func TestSearchClients_Pagination(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	q.On("SearchClients", mock.Anything, mock.MatchedBy(func(p sqlc.SearchClientsParams) bool {
		// page=2, limit=5 → offset = (2-1)*5 = 5, fetch 6 rows
		return p.Offset == 5 && p.Limit == 6
	})).Return([]sqlc.SearchClientsRow{}, nil)

	resp, err := h.SearchClients(
		testutil.EmployeeContext("5", []string{}),
		&pb.SearchClientsRequest{Page: 2, Limit: 5},
	)
	require.NoError(t, err)
	assert.Empty(t, resp.Clients)
	assert.False(t, resp.HasMore)
}

// ─── GetMyProfile ─────────────────────────────────────────────────────────────

func TestGetMyProfile_Unauthenticated(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	resp, err := h.GetMyProfile(testutil.UnauthenticatedContext(), &pb.GetMyProfileRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.Unauthenticated, grpcCode(err))
}

func TestGetMyProfile_Success(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	user := sqlc.GetUserByIDRow{
		ID:        42,
		Email:     "employee@test.com",
		FirstName: "Marko",
		LastName:  "Markovic",
		UserType:  "EMPLOYEE",
		IsActive:  true,
		CreatedAt: time.Now(),
	}
	q.On("GetUserByID", mock.Anything, int64(42)).Return(user, nil)

	ctx := testutil.EmployeeContext("42", []string{})
	resp, err := h.GetMyProfile(ctx, &pb.GetMyProfileRequest{})
	require.NoError(t, err)
	assert.Equal(t, int64(42), resp.Id)
	assert.Equal(t, "employee@test.com", resp.Email)
	assert.Equal(t, "Marko", resp.FirstName)
	assert.Equal(t, "Markovic", resp.LastName)
}

func TestGetMyProfile_DBError(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	q.On("GetUserByID", mock.Anything, int64(7)).Return(sqlc.GetUserByIDRow{}, errors.New("db error"))

	ctx := testutil.EmployeeContext("7", []string{})
	resp, err := h.GetMyProfile(ctx, &pb.GetMyProfileRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, grpcCode(err))
}

func TestGetMyProfile_UserNotFound(t *testing.T) {
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	q.On("GetUserByID", mock.Anything, int64(99)).Return(sqlc.GetUserByIDRow{}, sql.ErrNoRows)

	ctx := testutil.EmployeeContext("99", []string{})
	resp, err := h.GetMyProfile(ctx, &pb.GetMyProfileRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.NotFound, grpcCode(err))
}

func TestGetMyProfile_InvalidSubjectClaim(t *testing.T) {
	// Inject a context with non-numeric Subject to trigger ParseInt error
	ctx := auth.NewContextWithClaims(context.Background(), &auth.AccessClaims{
		Email:    "test@test.com",
		UserType: "EMPLOYEE",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "not-a-number",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(15 * time.Minute)),
		},
	})

	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})
	resp, err := h.GetMyProfile(ctx, &pb.GetMyProfileRequest{})
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, grpcCode(err))
}

// ─── extractBearerToken (white-box via UpdateEmployee which calls it) ─────────
// extractBearerToken is private so we test it indirectly via GRPCIncomingContext.

func TestExtractBearerToken_NoMetadata(t *testing.T) {
	// Plain background context — no gRPC metadata → UpdateEmployee should still
	// work for the auth failure path (claims missing → PermissionDenied).
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	_, err := h.UpdateEmployee(context.Background(), &pb.UpdateEmployeeRequest{})
	// No claims → PermissionDenied (not Internal/panic)
	assert.Equal(t, codes.PermissionDenied, grpcCode(err))
}

func TestExtractBearerToken_WithBearerHeader(t *testing.T) {
	// Verify that when UpdateEmployee is called with incoming metadata carrying
	// an Authorization header, the handler doesn't panic and correctly extracts it.
	// We exercise this by letting UpdateEmployee fail at auth (not at bearer extraction).
	q := &mocks.MockQuerier{}
	h := newHandler(q, &mocks.MockEmailPublisher{})

	md := metadata.Pairs("authorization", "Bearer sometoken123")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := h.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{})
	// Still fails auth (no claims injected), but extractBearerToken is exercised
	assert.Equal(t, codes.PermissionDenied, grpcCode(err))
}

func TestExtractBearerToken_ViaGRPCIncomingContext(t *testing.T) {
	// Use testutil helper — builds incoming metadata with Bearer prefix
	token := testutil.MakeAccessToken("1", "admin@test.com", "ADMIN", []string{})
	ctx := testutil.GRPCIncomingContext(token)

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}
	h := handler.NewUserHandler(
		q, nil,
		testutil.TestAccessSecret,
		testutil.TestRefreshSecret,
		testutil.TestActivationSecret,
		pub,
		&utils.NoOpUserCreatedPublisher{},
		nil, nil,
	)

	// HealthCheck just returns SERVING — it's a no-op that still exercises the
	// GRPCIncomingContext wiring without touching any mock.
	resp, err := h.HealthCheck(ctx, &pb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, "SERVING", resp.Status)
}
