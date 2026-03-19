package handler_test

// Transaction-backed tests for CreateEmployee and UpdateEmployee.
// Uses go-sqlmock to mock sql.DB/sql.Tx so the full handler path
// (including BeginTx → INSERT/UPDATE → Commit) is exercised.

import (
	"database/sql"
	"regexp"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	pb "banka-backend/proto/user"
	sqlc "banka-backend/services/user-service/internal/database/sqlc"
	"banka-backend/services/user-service/internal/handler"
	"banka-backend/services/user-service/internal/testutil"
	"banka-backend/services/user-service/mocks"
)

// newTxHandler builds a handler that uses a real (sqlmocked) *sql.DB.
func newTxHandler(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher, db *sql.DB) *handler.UserHandler {
	return handler.NewUserHandler(
		q, db,
		testutil.TestAccessSecret,
		testutil.TestRefreshSecret,
		testutil.TestActivationSecret,
		pub,
		nil, // clientSvc — not exercised by transaction tests
	)
}

// pgDupErr returns a postgres unique-violation error (SQLSTATE 23505).
func pgDupErr() error {
	return &pgconn.PgError{Code: "23505"}
}

// ─── CreateEmployee ───────────────────────────────────────────────────────────

func TestCreateEmployee_Success(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "user_type", "first_name", "last_name", "is_active", "created_at"}).
			AddRow(int64(1), "emp@test.com", "EMPLOYEE", "Alice", "Smith", true, time.Now()))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO employee_details")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_permissions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	smock.ExpectCommit()

	pub.On("Publish", mock.Anything).Return(nil)

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	resp, err := h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:       "emp@test.com",
		FirstName:   "Alice",
		LastName:    "Smith",
		Permissions: []string{"VIEW_ACCOUNTS"},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), resp.Id)
	assert.Equal(t, "emp@test.com", resp.Email)
	require.NoError(t, smock.ExpectationsWereMet())
}

func TestCreateEmployee_DuplicateEmail(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnError(pgDupErr())
	smock.ExpectRollback()

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	_, err = h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:     "taken@test.com",
		FirstName: "Bob",
		LastName:  "Jones",
	})
	assert.Equal(t, codes.AlreadyExists, grpcCode(err))
	require.NoError(t, smock.ExpectationsWereMet())
}

func TestCreateEmployee_CreateUserInternalError(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnError(sql.ErrConnDone)
	smock.ExpectRollback()

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	_, err = h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:     "emp@test.com",
		FirstName: "Bob",
		LastName:  "Jones",
	})
	assert.Equal(t, codes.Internal, grpcCode(err))
}

func TestCreateEmployee_EmployeeDetailsError(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "user_type", "first_name", "last_name", "is_active", "created_at"}).
			AddRow(int64(2), "emp@test.com", "EMPLOYEE", "Bob", "Jones", true, time.Now()))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO employee_details")).
		WillReturnError(pgDupErr()) // username taken
	smock.ExpectRollback()

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	_, err = h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:     "emp@test.com",
		FirstName: "Bob",
		LastName:  "Jones",
		Username:  "bob.jones",
	})
	assert.Equal(t, codes.AlreadyExists, grpcCode(err))
}

func TestCreateEmployee_CommitError(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "user_type", "first_name", "last_name", "is_active", "created_at"}).
			AddRow(int64(3), "emp@test.com", "EMPLOYEE", "Carol", "White", true, time.Now()))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO employee_details")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	smock.ExpectCommit().WillReturnError(sql.ErrTxDone)

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	_, err = h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:     "emp@test.com",
		FirstName: "Carol",
		LastName:  "White",
	})
	assert.Equal(t, codes.Internal, grpcCode(err))
}

func TestCreateEmployee_AdminType_SkipsPermissions(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	smock.ExpectBegin()
	smock.ExpectQuery(regexp.QuoteMeta("INSERT INTO users")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "user_type", "first_name", "last_name", "is_active", "created_at"}).
			AddRow(int64(4), "admin2@test.com", "ADMIN", "Dan", "Brown", true, time.Now()))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO employee_details")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	// No permission INSERT expected — ADMIN skips the loop
	smock.ExpectCommit()

	pub.On("Publish", mock.Anything).Return(nil)

	h := newTxHandler(q, pub, db)
	ctx := testutil.AdminContext()

	resp, err := h.CreateEmployee(ctx, &pb.CreateEmployeeRequest{
		Email:     "admin2@test.com",
		FirstName: "Dan",
		LastName:  "Brown",
		UserType:  pb.UserType_USER_TYPE_ADMIN,
		// Permissions are ignored for ADMIN
		Permissions: []string{"VIEW_ACCOUNTS"},
	})
	require.NoError(t, err)
	assert.NotNil(t, resp)
	require.NoError(t, smock.ExpectationsWereMet())
}

// ─── UpdateEmployee ───────────────────────────────────────────────────────────

func TestUpdateEmployee_Success(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	ctx := testutil.AdminContext()

	existing := sqlc.GetEmployeeByIDRow{
		ID: 5, Email: "old@test.com", FirstName: "Eve", LastName: "Davis",
		UserType: "EMPLOYEE", IsActive: true, CreatedAt: time.Now(),
		Username: "eve.davis",
	}
	updated := sqlc.GetEmployeeByIDRow{
		ID: 5, Email: "new@test.com", FirstName: "Eve", LastName: "Davis",
		UserType: "EMPLOYEE", IsActive: true, CreatedAt: time.Now(),
		Username: "eve.davis",
	}

	// Pre-fetch (outside tx)
	q.On("GetEmployeeByID", ctx, int64(5)).Return(existing, nil).Once()
	// Post-commit re-read
	q.On("GetEmployeeByID", ctx, int64(5)).Return(updated, nil).Once()
	q.On("GetUserPermissions", ctx, int64(5)).Return([]string{"VIEW_ACCOUNTS"}, nil)

	smock.ExpectBegin()
	smock.ExpectExec(regexp.QuoteMeta("UPDATE users")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectExec(regexp.QuoteMeta("UPDATE employee_details")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_permissions")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectExec(regexp.QuoteMeta("INSERT INTO user_permissions")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	smock.ExpectCommit()

	h := newTxHandler(q, pub, db)

	resp, err := h.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{
		Id:          5,
		Email:       "new@test.com",
		FirstName:   "Eve",
		LastName:    "Davis",
		Permissions: []string{"VIEW_ACCOUNTS"},
	})
	require.NoError(t, err)
	assert.Equal(t, "new@test.com", resp.Employee.User.Email)
	require.NoError(t, smock.ExpectationsWereMet())
	q.AssertExpectations(t)
}

func TestUpdateEmployee_DuplicateEmail(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	ctx := testutil.AdminContext()

	existing := sqlc.GetEmployeeByIDRow{ID: 6, UserType: "EMPLOYEE", Email: "a@test.com", FirstName: "X", LastName: "Y", CreatedAt: time.Now()}
	q.On("GetEmployeeByID", ctx, int64(6)).Return(existing, nil)

	smock.ExpectBegin()
	smock.ExpectExec(regexp.QuoteMeta("UPDATE users")).
		WillReturnError(pgDupErr())
	smock.ExpectRollback()

	h := newTxHandler(q, pub, db)

	_, err = h.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{
		Id:        6,
		Email:     "taken@test.com",
		FirstName: "X",
		LastName:  "Y",
	})
	assert.Equal(t, codes.AlreadyExists, grpcCode(err))
}

func TestUpdateEmployee_CommitError(t *testing.T) {
	db, smock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	q := &mocks.MockQuerier{}
	pub := &mocks.MockEmailPublisher{}

	ctx := testutil.AdminContext()

	existing := sqlc.GetEmployeeByIDRow{ID: 7, UserType: "EMPLOYEE", Email: "u@test.com", FirstName: "G", LastName: "H", CreatedAt: time.Now()}
	q.On("GetEmployeeByID", ctx, int64(7)).Return(existing, nil)

	smock.ExpectBegin()
	smock.ExpectExec(regexp.QuoteMeta("UPDATE users")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectExec(regexp.QuoteMeta("UPDATE employee_details")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectExec(regexp.QuoteMeta("DELETE FROM user_permissions")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	smock.ExpectCommit().WillReturnError(sql.ErrTxDone)

	h := newTxHandler(q, pub, db)

	_, err = h.UpdateEmployee(ctx, &pb.UpdateEmployeeRequest{
		Id:        7,
		Email:     "u@test.com",
		FirstName: "G",
		LastName:  "H",
	})
	assert.Equal(t, codes.Internal, grpcCode(err))
}
