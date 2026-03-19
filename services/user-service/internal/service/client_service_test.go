package service_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sqlc "banka-backend/services/user-service/internal/database/sqlc"
	"banka-backend/services/user-service/internal/domain"
	"banka-backend/services/user-service/internal/service"
	"banka-backend/services/user-service/mocks"
)

// newClientService wires a clientService with a mock querier (the repository).
func newClientService(q *mocks.MockQuerier) domain.ClientService {
	return service.NewClientService(q)
}

// clientRow builds a minimal GetUserByIDRow for a CLIENT user.
func clientRow(id int64) sqlc.GetUserByIDRow {
	return sqlc.GetUserByIDRow{
		ID:          id,
		Email:       "client@test.com",
		UserType:    "CLIENT",
		FirstName:   "Ana",
		LastName:    "Petrović",
		BirthDate:   946684800000, // 2000-01-01 00:00:00 UTC in ms
		Gender:      sql.NullString{String: "FEMALE", Valid: true},
		PhoneNumber: sql.NullString{String: "+381601234567", Valid: true},
		Address:     sql.NullString{String: "Knez Mihailova 1, Beograd", Valid: true},
		IsActive:    true,
		CreatedAt:   time.Now(),
	}
}

// ─── GetClientByID — happy path ───────────────────────────────────────────────

func TestGetClientByID_Success(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)

	row := clientRow(42)
	q.On("GetUserByID", context.Background(), int64(42)).Return(row, nil)

	got, err := svc.GetClientByID(context.Background(), 42)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, int64(42), got.ID)
	assert.Equal(t, "Ana", got.FirstName)
	assert.Equal(t, "Petrović", got.LastName)
	assert.Equal(t, "client@test.com", got.Email)
	assert.Equal(t, "+381601234567", got.PhoneNumber)
	assert.Equal(t, "Knez Mihailova 1, Beograd", got.Address)
	assert.Equal(t, int64(946684800000), got.DateOfBirth)
	assert.Equal(t, "FEMALE", got.Gender)

	q.AssertExpectations(t)
}

// ─── GetClientByID — nullable optional fields ─────────────────────────────────

func TestGetClientByID_NullableFieldsReturnEmptyString(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)

	row := clientRow(7)
	row.PhoneNumber = sql.NullString{Valid: false}
	row.Address = sql.NullString{Valid: false}
	row.Gender = sql.NullString{Valid: false}
	q.On("GetUserByID", context.Background(), int64(7)).Return(row, nil)

	got, err := svc.GetClientByID(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "", got.PhoneNumber)
	assert.Equal(t, "", got.Address)
	assert.Equal(t, "", got.Gender)
	q.AssertExpectations(t)
}

// ─── GetClientByID — not found ────────────────────────────────────────────────

func TestGetClientByID_NotFound_NoRows(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)

	q.On("GetUserByID", context.Background(), int64(999)).Return(sqlc.GetUserByIDRow{}, sql.ErrNoRows)

	_, err := svc.GetClientByID(context.Background(), 999)
	assert.ErrorIs(t, err, domain.ErrClientNotFound)
	q.AssertExpectations(t)
}

// ─── GetClientByID — wrong user_type ─────────────────────────────────────────

func TestGetClientByID_NotFound_WrongUserType(t *testing.T) {
	tests := []struct {
		name     string
		userType string
	}{
		{"employee row", "EMPLOYEE"},
		{"admin row", "ADMIN"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			svc := newClientService(q)

			row := clientRow(10)
			row.UserType = tc.userType
			q.On("GetUserByID", context.Background(), int64(10)).Return(row, nil)

			_, err := svc.GetClientByID(context.Background(), 10)
			assert.ErrorIs(t, err, domain.ErrClientNotFound,
				"user_type %q must not be exposed through the client endpoint", tc.userType)
			q.AssertExpectations(t)
		})
	}
}

// ─── GetClientByID — database error ──────────────────────────────────────────

func TestGetClientByID_DBError(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)

	dbErr := errors.New("connection refused")
	q.On("GetUserByID", context.Background(), int64(1)).Return(sqlc.GetUserByIDRow{}, dbErr)

	_, err := svc.GetClientByID(context.Background(), 1)
	require.Error(t, err)
	assert.False(t, errors.Is(err, domain.ErrClientNotFound), "raw DB error must not be wrapped as ErrClientNotFound")
	q.AssertExpectations(t)
}

// ─── UpdateClient ─────────────────────────────────────────────────────────────

// existingClientRow returns a full GetUserByIDRow representing a CLIENT.
func existingClientRow() sqlc.GetUserByIDRow {
	return sqlc.GetUserByIDRow{
		ID:          42,
		Email:       "ana@test.com",
		UserType:    "CLIENT",
		FirstName:   "Ana",
		LastName:    "Petrović",
		BirthDate:   946684800000,
		Gender:      sql.NullString{String: "FEMALE", Valid: true},
		PhoneNumber: sql.NullString{String: "+381601234567", Valid: true},
		Address:     sql.NullString{String: "Knez Mihailova 1", Valid: true},
		IsActive:    true,
		CreatedAt:   time.Now(),
	}
}

func TestUpdateClient_Success(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	row := existingClientRow()
	q.On("GetUserByID", ctx, int64(42)).Return(row, nil)
	q.On("UpdateUser", ctx, sqlc.UpdateUserParams{
		ID:          42,
		Email:       "new@test.com",
		FirstName:   "Ana",
		LastName:    "Petrović",
		BirthDate:   row.BirthDate,
		Gender:      row.Gender,
		PhoneNumber: sql.NullString{String: "+381611111111", Valid: true},
		Address:     row.Address,
		IsActive:    row.IsActive,
	}).Return(nil)

	got, err := svc.UpdateClient(ctx, 42, domain.UpdateClientInput{
		Email:       "new@test.com",
		PhoneNumber: "+381611111111",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.Equal(t, "new@test.com", got.Email)
	assert.Equal(t, "+381611111111", got.PhoneNumber)
	// Unchanged fields are preserved
	assert.Equal(t, "Ana", got.FirstName)
	assert.Equal(t, "Petrović", got.LastName)
	assert.Equal(t, "Knez Mihailova 1", got.Address)
	q.AssertExpectations(t)
}

func TestUpdateClient_NotFound_NoRows(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	q.On("GetUserByID", ctx, int64(999)).Return(sqlc.GetUserByIDRow{}, sql.ErrNoRows)

	_, err := svc.UpdateClient(ctx, 999, domain.UpdateClientInput{FirstName: "X"})
	assert.ErrorIs(t, err, domain.ErrClientNotFound)
	q.AssertExpectations(t)
}

func TestUpdateClient_NotFound_WrongUserType(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	row := existingClientRow()
	row.UserType = "EMPLOYEE"
	q.On("GetUserByID", ctx, int64(42)).Return(row, nil)

	_, err := svc.UpdateClient(ctx, 42, domain.UpdateClientInput{FirstName: "X"})
	assert.ErrorIs(t, err, domain.ErrClientNotFound)
	q.AssertExpectations(t)
}

func TestUpdateClient_EmailAlreadyTaken(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	row := existingClientRow()
	q.On("GetUserByID", ctx, int64(42)).Return(row, nil)
	q.On("UpdateUser", ctx, sqlc.UpdateUserParams{
		ID:          42,
		Email:       "taken@test.com",
		FirstName:   row.FirstName,
		LastName:    row.LastName,
		BirthDate:   row.BirthDate,
		Gender:      row.Gender,
		PhoneNumber: row.PhoneNumber,
		Address:     row.Address,
		IsActive:    row.IsActive,
	}).Return(&pgconn.PgError{Code: "23505"})

	_, err := svc.UpdateClient(ctx, 42, domain.UpdateClientInput{Email: "taken@test.com"})
	assert.ErrorIs(t, err, domain.ErrEmailTaken)
	q.AssertExpectations(t)
}

// ─── ListClients ──────────────────────────────────────────────────────────────

// makeClientRows returns n minimal ListClientsRow values.
func makeClientRows(n int) []sqlc.ListClientsRow {
	rows := make([]sqlc.ListClientsRow, n)
	for i := range rows {
		rows[i] = sqlc.ListClientsRow{
			ID:          int64(i + 1),
			FirstName:   "First",
			LastName:    fmt.Sprintf("Last%02d", i),
			Email:       fmt.Sprintf("client%d@test.com", i),
			PhoneNumber: sql.NullString{String: "+381601234567", Valid: true},
		}
	}
	return rows
}

func TestListClients_NoFilter(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	// default limit 20 → fetch 21
	q.On("ListClients", ctx, sqlc.ListClientsParams{
		Limit:  21,
		Offset: 0,
		Name:   sql.NullString{Valid: false},
		Email:  sql.NullString{Valid: false},
	}).Return(makeClientRows(5), nil)

	got, hasMore, err := svc.ListClients(ctx, domain.ClientFilter{})
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Len(t, got, 5)
	assert.Equal(t, "+381601234567", got[0].PhoneNumber)
	q.AssertExpectations(t)
}

func TestListClients_FilterByName(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	q.On("ListClients", ctx, sqlc.ListClientsParams{
		Limit:  6, // limit=5 → fetch 6
		Offset: 0,
		Name:   sql.NullString{String: "Petrović", Valid: true},
		Email:  sql.NullString{Valid: false},
	}).Return(makeClientRows(3), nil)

	got, hasMore, err := svc.ListClients(ctx, domain.ClientFilter{Name: "Petrović", Limit: 5})
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Len(t, got, 3)
	q.AssertExpectations(t)
}

func TestListClients_FilterByEmail(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	q.On("ListClients", ctx, sqlc.ListClientsParams{
		Limit:  6,
		Offset: 0,
		Name:   sql.NullString{Valid: false},
		Email:  sql.NullString{String: "@bank.com", Valid: true},
	}).Return(makeClientRows(2), nil)

	got, hasMore, err := svc.ListClients(ctx, domain.ClientFilter{Email: "@bank.com", Limit: 5})
	require.NoError(t, err)
	assert.False(t, hasMore)
	assert.Len(t, got, 2)
	q.AssertExpectations(t)
}

func TestListClients_HasMore(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	// 3 rows requested → fetch 4; DB returns 4 → has_more=true, trimmed to 3
	q.On("ListClients", ctx, sqlc.ListClientsParams{
		Limit:  4,
		Offset: 0,
		Name:   sql.NullString{Valid: false},
		Email:  sql.NullString{Valid: false},
	}).Return(makeClientRows(4), nil)

	got, hasMore, err := svc.ListClients(ctx, domain.ClientFilter{Limit: 3})
	require.NoError(t, err)
	assert.True(t, hasMore)
	assert.Len(t, got, 3)
	q.AssertExpectations(t)
}

func TestListClients_DBError(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	q.On("ListClients", ctx, sqlc.ListClientsParams{
		Limit: 21, Offset: 0,
		Name: sql.NullString{Valid: false}, Email: sql.NullString{Valid: false},
	}).Return(nil, errors.New("db down"))

	_, _, err := svc.ListClients(ctx, domain.ClientFilter{})
	require.Error(t, err)
	q.AssertExpectations(t)
}

func TestUpdateClient_DBError(t *testing.T) {
	q := &mocks.MockQuerier{}
	svc := newClientService(q)
	ctx := context.Background()

	row := existingClientRow()
	q.On("GetUserByID", ctx, int64(42)).Return(row, nil)
	q.On("UpdateUser", ctx, sqlc.UpdateUserParams{
		ID:          42,
		Email:       row.Email,
		FirstName:   row.FirstName,
		LastName:    row.LastName,
		BirthDate:   row.BirthDate,
		Gender:      row.Gender,
		PhoneNumber: row.PhoneNumber,
		Address:     row.Address,
		IsActive:    row.IsActive,
	}).Return(errors.New("connection refused"))

	_, err := svc.UpdateClient(ctx, 42, domain.UpdateClientInput{})
	require.Error(t, err)
	assert.False(t, errors.Is(err, domain.ErrEmailTaken))
	assert.False(t, errors.Is(err, domain.ErrClientNotFound))
	q.AssertExpectations(t)
}
