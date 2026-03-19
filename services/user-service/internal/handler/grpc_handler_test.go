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

	pb "banka-backend/proto/user"
	auth "banka-backend/shared/auth"
	sqlc "banka-backend/services/user-service/internal/database/sqlc"
	"banka-backend/services/user-service/internal/domain"
	"banka-backend/services/user-service/internal/handler"
	"banka-backend/services/user-service/internal/testutil"
	utils "banka-backend/services/user-service/internal/utils"
	"banka-backend/services/user-service/mocks"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newHandler builds a UserHandler with mock dependencies.
// sqlDB is nil for tests that never reach BeginTx (validation/auth early exits).
// clientSvc is nil — use newHandlerWithClientSvc for GetClientByID tests.
func newHandler(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) *handler.UserHandler {
	return handler.NewUserHandler(
		q, nil,
		testutil.TestAccessSecret,
		testutil.TestRefreshSecret,
		testutil.TestActivationSecret,
		pub,
		nil, // clientSvc — not needed by the existing tests
	)
}

// newHandlerWithClientSvc builds a UserHandler wired with a mock ClientService.
// Use this for GetClientByID tests that need the service layer exercised.
func newHandlerWithClientSvc(clientSvc *mocks.MockClientService) *handler.UserHandler {
	return handler.NewUserHandler(
		&mocks.MockQuerier{}, nil,
		testutil.TestAccessSecret,
		testutil.TestRefreshSecret,
		testutil.TestActivationSecret,
		&mocks.MockEmailPublisher{},
		clientSvc,
	)
}

func bcryptHash(plain string) string {
	b, _ := bcrypt.GenerateFromPassword([]byte(plain), 12)
	return string(b)
}

func grpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}
	s, _ := status.FromError(err)
	return s.Code()
}

// ─── HealthCheck ──────────────────────────────────────────────────────────────

func TestHealthCheck(t *testing.T) {
	h := newHandler(&mocks.MockQuerier{}, &mocks.MockEmailPublisher{})
	resp, err := h.HealthCheck(context.Background(), &pb.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, "SERVING", resp.Status)
}

// ─── Login ────────────────────────────────────────────────────────────────────

func TestLogin(t *testing.T) {
	hash := bcryptHash("ValidPass1!")
	activeUser := sqlc.User{
		ID:           1,
		Email:        "user@test.com",
		PasswordHash: hash,
		UserType:     "EMPLOYEE",
		IsActive:     true,
	}

	tests := []struct {
		name      string
		setup     func(q *mocks.MockQuerier)
		req       *pb.LoginRequest
		wantCode  codes.Code
		checkResp func(t *testing.T, r *pb.LoginResponse)
	}{
		{
			name: "success",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "user@test.com").Return(activeUser, nil)
				q.On("GetUserPermissions", context.Background(), int64(1)).Return([]string{"VIEW_ACCOUNTS"}, nil)
			},
			req:      &pb.LoginRequest{Email: "user@test.com", Password: "ValidPass1!"},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.LoginResponse) {
				assert.NotEmpty(t, r.AccessToken)
				assert.NotEmpty(t, r.RefreshToken)
				assert.Equal(t, "Bearer", r.TokenType)
				assert.EqualValues(t, 900, r.ExpiresIn)
			},
		},
		{
			name: "user not found",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "ghost@test.com").Return(sqlc.User{}, sql.ErrNoRows)
			},
			req:      &pb.LoginRequest{Email: "ghost@test.com", Password: "anypass"},
			wantCode: codes.NotFound,
		},
		{
			name: "account inactive",
			setup: func(q *mocks.MockQuerier) {
				inactive := activeUser
				inactive.IsActive = false
				q.On("GetUserByEmail", context.Background(), "user@test.com").Return(inactive, nil)
			},
			req:      &pb.LoginRequest{Email: "user@test.com", Password: "ValidPass1!"},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "wrong password",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "user@test.com").Return(activeUser, nil)
			},
			req:      &pb.LoginRequest{Email: "user@test.com", Password: "WrongPassword!"},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "db error",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "user@test.com").Return(sqlc.User{}, errors.New("db error"))
			},
			req:      &pb.LoginRequest{Email: "user@test.com", Password: "any"},
			wantCode: codes.Internal,
		},
		{
			name: "permissions fetch error",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "user@test.com").Return(activeUser, nil)
				q.On("GetUserPermissions", context.Background(), int64(1)).Return(nil, errors.New("perm error"))
			},
			req:      &pb.LoginRequest{Email: "user@test.com", Password: "ValidPass1!"},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			resp, err := h.Login(context.Background(), tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				if tc.checkResp != nil {
					tc.checkResp(t, resp)
				}
			} else {
				assert.Nil(t, resp)
			}
			q.AssertExpectations(t)
		})
	}
}

// ─── RefreshToken ─────────────────────────────────────────────────────────────

func TestRefreshToken(t *testing.T) {
	activeUser := sqlc.GetUserByIDRow{
		ID:       5,
		Email:    "user@test.com",
		UserType: "EMPLOYEE",
		IsActive: true,
	}

	validRefresh := func() string {
		_, r, _ := auth.GenerateTokens("5", "user@test.com", "EMPLOYEE", nil,
			testutil.TestAccessSecret, testutil.TestRefreshSecret)
		return r
	}

	tests := []struct {
		name     string
		setup    func(q *mocks.MockQuerier)
		token    string
		wantCode codes.Code
	}{
		{
			name: "success",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByID", context.Background(), int64(5)).Return(activeUser, nil)
				q.On("GetUserPermissions", context.Background(), int64(5)).Return([]string{"VIEW_ACCOUNTS"}, nil)
			},
			token:    validRefresh(),
			wantCode: codes.OK,
		},
		{
			name:     "invalid token",
			setup:    func(q *mocks.MockQuerier) {},
			token:    "not-a-valid-token",
			wantCode: codes.Unauthenticated,
		},
		{
			name: "user no longer exists",
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByID", context.Background(), int64(5)).Return(sqlc.GetUserByIDRow{}, sql.ErrNoRows)
			},
			token:    validRefresh(),
			wantCode: codes.NotFound,
		},
		{
			name: "account inactive",
			setup: func(q *mocks.MockQuerier) {
				inactive := activeUser
				inactive.IsActive = false
				q.On("GetUserByID", context.Background(), int64(5)).Return(inactive, nil)
			},
			token:    validRefresh(),
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			resp, err := h.RefreshToken(context.Background(), &pb.RefreshTokenRequest{RefreshToken: tc.token})
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				assert.NotEmpty(t, resp.AccessToken)
				assert.Equal(t, tc.token, resp.RefreshToken) // original token echoed back
			}
			q.AssertExpectations(t)
		})
	}
}

// ─── GetAllPermissions ────────────────────────────────────────────────────────

func TestGetAllPermissions(t *testing.T) {
	perms := []sqlc.Permission{
		{ID: 1, PermissionCode: "ADMIN_PERMISSION"},
		{ID: 2, PermissionCode: "VIEW_ACCOUNTS"},
		{ID: 3, PermissionCode: "MANAGE_ACCOUNTS"},
	}

	tests := []struct {
		name     string
		ctx      context.Context
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
		wantLen  int
	}{
		{
			name: "admin gets permissions (ADMIN_PERMISSION filtered out)",
			ctx:  testutil.AdminContext(),
			setup: func(q *mocks.MockQuerier) {
				q.On("GetAllPermissions", testutil.AdminContext()).Return(perms, nil)
			},
			wantCode: codes.OK,
			wantLen:  2,
		},
		{
			name:     "non-admin denied",
			ctx:      testutil.EmployeeContext("2", nil),
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "db error",
			ctx:  testutil.AdminContext(),
			setup: func(q *mocks.MockQuerier) {
				q.On("GetAllPermissions", testutil.AdminContext()).Return(nil, errors.New("db error"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			resp, err := h.GetAllPermissions(tc.ctx, &pb.GetAllPermissionsRequest{})
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				assert.Len(t, resp.Permissions, tc.wantLen)
			}
			q.AssertExpectations(t)
		})
	}
}

// ─── GetAllEmployees ──────────────────────────────────────────────────────────

func TestGetAllEmployees(t *testing.T) {
	rows := []sqlc.ListEmployeesRow{
		{ID: 1, Email: "a@b.com", FirstName: "Alice", LastName: "A", UserType: "EMPLOYEE", IsActive: true},
	}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *pb.GetAllEmployeesRequest
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
	}{
		{
			name: "admin lists employees",
			ctx:  testutil.AdminContext(),
			req:  &pb.GetAllEmployeesRequest{Page: 1, PageSize: 10},
			setup: func(q *mocks.MockQuerier) {
				q.On("ListEmployees", testutil.AdminContext(), sqlc.ListEmployeesParams{Limit: 10, Offset: 0}).
					Return(rows, nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "non-admin denied",
			ctx:      testutil.EmployeeContext("2", nil),
			req:      &pb.GetAllEmployeesRequest{},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "pagination defaults applied (page=0 → page=1)",
			ctx:  testutil.AdminContext(),
			req:  &pb.GetAllEmployeesRequest{Page: 0, PageSize: 0},
			setup: func(q *mocks.MockQuerier) {
				q.On("ListEmployees", testutil.AdminContext(), sqlc.ListEmployeesParams{Limit: 10, Offset: 0}).
					Return([]sqlc.ListEmployeesRow{}, nil)
			},
			wantCode: codes.OK,
		},
		{
			name: "db error",
			ctx:  testutil.AdminContext(),
			req:  &pb.GetAllEmployeesRequest{Page: 1, PageSize: 5},
			setup: func(q *mocks.MockQuerier) {
				q.On("ListEmployees", testutil.AdminContext(), sqlc.ListEmployeesParams{Limit: 5, Offset: 0}).
					Return(nil, errors.New("db"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			_, err := h.GetAllEmployees(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			q.AssertExpectations(t)
		})
	}
}

// ─── GetEmployeeByID ──────────────────────────────────────────────────────────

func TestGetEmployeeByID(t *testing.T) {
	employeeRow := sqlc.GetEmployeeByIDRow{
		ID:       10,
		Email:    "emp@test.com",
		UserType: "EMPLOYEE",
		IsActive: true,
		Username: "emp.user",
	}

	tests := []struct {
		name     string
		ctx      context.Context
		id       int64
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
	}{
		{
			name: "admin fetches employee",
			ctx:  testutil.AdminContext(),
			id:   10,
			setup: func(q *mocks.MockQuerier) {
				q.On("GetEmployeeByID", testutil.AdminContext(), int64(10)).Return(employeeRow, nil)
				q.On("GetUserPermissions", testutil.AdminContext(), int64(10)).Return([]string{"VIEW_ACCOUNTS"}, nil)
			},
			wantCode: codes.OK,
		},
		{
			name: "manage_users permission allows access",
			ctx:  testutil.EmployeeContext("2", []string{"MANAGE_USERS"}),
			id:   10,
			setup: func(q *mocks.MockQuerier) {
				ctx := testutil.EmployeeContext("2", []string{"MANAGE_USERS"})
				q.On("GetEmployeeByID", ctx, int64(10)).Return(employeeRow, nil)
				q.On("GetUserPermissions", ctx, int64(10)).Return([]string{}, nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "no auth claims → denied",
			ctx:      testutil.UnauthenticatedContext(),
			id:       10,
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "plain employee without manage_users → denied",
			ctx:      testutil.EmployeeContext("2", nil),
			id:       10,
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "employee not found",
			ctx:  testutil.AdminContext(),
			id:   999,
			setup: func(q *mocks.MockQuerier) {
				q.On("GetEmployeeByID", testutil.AdminContext(), int64(999)).Return(sqlc.GetEmployeeByIDRow{}, sql.ErrNoRows)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "admin target blocked",
			ctx:  testutil.AdminContext(),
			id:   20,
			setup: func(q *mocks.MockQuerier) {
				adminRow := employeeRow
				adminRow.UserType = "ADMIN"
				q.On("GetEmployeeByID", testutil.AdminContext(), int64(20)).Return(adminRow, nil)
			},
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			_, err := h.GetEmployeeByID(tc.ctx, &pb.GetEmployeeByIDRequest{Id: tc.id})
			assert.Equal(t, tc.wantCode, grpcCode(err))
			q.AssertExpectations(t)
		})
	}
}

// ─── ToggleEmployeeActive ─────────────────────────────────────────────────────

func TestToggleEmployeeActive(t *testing.T) {
	empRow := sqlc.GetUserByIDRow{ID: 5, Email: "e@test.com", UserType: "EMPLOYEE", IsActive: true}

	tests := []struct {
		name     string
		ctx      context.Context
		req      *pb.ToggleEmployeeActiveRequest
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
	}{
		{
			name: "admin deactivates employee",
			ctx:  testutil.AdminContext(),
			req:  &pb.ToggleEmployeeActiveRequest{Id: 5, IsActive: false},
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByID", testutil.AdminContext(), int64(5)).Return(empRow, nil)
				q.On("UpdateUserActive", testutil.AdminContext(), sqlc.UpdateUserActiveParams{ID: 5, IsActive: false}).Return(nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "no claims → denied",
			ctx:      testutil.UnauthenticatedContext(),
			req:      &pb.ToggleEmployeeActiveRequest{Id: 5},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "missing id",
			ctx:      testutil.AdminContext(),
			req:      &pb.ToggleEmployeeActiveRequest{Id: 0},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "user not found",
			ctx:  testutil.AdminContext(),
			req:  &pb.ToggleEmployeeActiveRequest{Id: 999},
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByID", testutil.AdminContext(), int64(999)).Return(sqlc.GetUserByIDRow{}, sql.ErrNoRows)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "cannot toggle client",
			ctx:  testutil.AdminContext(),
			req:  &pb.ToggleEmployeeActiveRequest{Id: 5},
			setup: func(q *mocks.MockQuerier) {
				clientRow := sqlc.GetUserByIDRow{ID: 5, UserType: "CLIENT"}
				q.On("GetUserByID", testutil.AdminContext(), int64(5)).Return(clientRow, nil)
			},
			wantCode: codes.PermissionDenied,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			_, err := h.ToggleEmployeeActive(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			q.AssertExpectations(t)
		})
	}
}

// ─── SetPassword ──────────────────────────────────────────────────────────────

func TestSetPassword(t *testing.T) {
	validToken, _ := auth.GenerateActivationToken("setpw@test.com", testutil.TestActivationSecret)

	tests := []struct {
		name     string
		req      *pb.SetPasswordRequest
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
	}{
		{
			name: "success",
			req:  &pb.SetPasswordRequest{Token: validToken, Password: "NewPass123!"},
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "setpw@test.com").
					Return(sqlc.User{ID: 1, Email: "setpw@test.com", PasswordHash: ""}, nil)
				q.On("UpdateUserPassword", context.Background(),
					matchUpdatePasswordEmail("setpw@test.com")).Return(nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "password too short",
			req:      &pb.SetPasswordRequest{Token: validToken, Password: "short"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid activation token",
			req:      &pb.SetPasswordRequest{Token: "bad-token", Password: "ValidPass1!"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "user not found",
			req:  &pb.SetPasswordRequest{Token: validToken, Password: "ValidPass1!"},
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "setpw@test.com").
					Return(sqlc.User{}, sql.ErrNoRows)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "already activated (password already set)",
			req:  &pb.SetPasswordRequest{Token: validToken, Password: "ValidPass1!"},
			setup: func(q *mocks.MockQuerier) {
				q.On("GetUserByEmail", context.Background(), "setpw@test.com").
					Return(sqlc.User{ID: 1, PasswordHash: "$2b$some-existing-hash"}, nil)
			},
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})

			_, err := h.SetPassword(context.Background(), tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err), "unexpected gRPC code: %v", err)
		})
	}
}

// ─── ActivateAccount ──────────────────────────────────────────────────────────

func TestActivateAccount(t *testing.T) {
	validToken, _ := auth.GenerateActivationToken("activate@test.com", testutil.TestActivationSecret)

	tests := []struct {
		name     string
		req      *pb.ActivateAccountRequest
		setup    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher)
		wantCode codes.Code
	}{
		{
			name: "success",
			req:  &pb.ActivateAccountRequest{Token: validToken, NewPassword: "NewPass123!", ConfirmPassword: "NewPass123!"},
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("GetUserByEmail", context.Background(), "activate@test.com").
					Return(sqlc.User{ID: 1, Email: "activate@test.com", PasswordHash: ""}, nil)
				q.On("UpdateUserPassword", context.Background(),
					matchUpdatePasswordEmail("activate@test.com")).Return(nil)
				pub.On("Publish", utils.EmailEvent{Type: "CONFIRMATION", Email: "activate@test.com", Token: ""}).
					Return(nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "passwords do not match",
			req:      &pb.ActivateAccountRequest{Token: validToken, NewPassword: "AAA", ConfirmPassword: "BBB"},
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "password too short",
			req:      &pb.ActivateAccountRequest{Token: validToken, NewPassword: "short", ConfirmPassword: "short"},
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "bad activation token",
			req:      &pb.ActivateAccountRequest{Token: "bad", NewPassword: "ValidPass1!", ConfirmPassword: "ValidPass1!"},
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "already activated",
			req:  &pb.ActivateAccountRequest{Token: validToken, NewPassword: "ValidPass1!", ConfirmPassword: "ValidPass1!"},
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("GetUserByEmail", context.Background(), "activate@test.com").
					Return(sqlc.User{ID: 1, PasswordHash: "$2b$existing"}, nil)
			},
			wantCode: codes.FailedPrecondition,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			pub := &mocks.MockEmailPublisher{}
			tc.setup(q, pub)
			h := newHandler(q, pub)

			_, err := h.ActivateAccount(context.Background(), tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
		})
	}
}

// ─── ForgotPassword ───────────────────────────────────────────────────────────

func TestForgotPassword(t *testing.T) {
	const safeMsg = "If your email is registered in our system, you will receive a password reset link."

	tests := []struct {
		name  string
		setup func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher)
		email string
	}{
		{
			name:  "active user: publishes reset event",
			email: "active@test.com",
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("GetUserByEmail", context.Background(), "active@test.com").
					Return(sqlc.User{ID: 1, IsActive: true, Email: "active@test.com"}, nil)
				pub.On("Publish", matchEmailType("RESET")).Return(nil)
			},
		},
		{
			name:  "inactive user: no publish",
			email: "inactive@test.com",
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("GetUserByEmail", context.Background(), "inactive@test.com").
					Return(sqlc.User{ID: 2, IsActive: false}, nil)
				// pub.Publish must NOT be called
			},
		},
		{
			name:  "unknown email: safe response, no publish",
			email: "ghost@test.com",
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("GetUserByEmail", context.Background(), "ghost@test.com").
					Return(sqlc.User{}, sql.ErrNoRows)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			pub := &mocks.MockEmailPublisher{}
			tc.setup(q, pub)
			h := newHandler(q, pub)

			resp, err := h.ForgotPassword(context.Background(), &pb.ForgotPasswordRequest{Email: tc.email})
			require.NoError(t, err) // always succeeds (anti-enumeration)
			assert.Equal(t, safeMsg, resp.Message)
			q.AssertExpectations(t)
			pub.AssertExpectations(t)
		})
	}
}

// ─── ResetPassword ────────────────────────────────────────────────────────────

func TestResetPassword(t *testing.T) {
	validToken, _ := auth.GenerateResetToken("reset@test.com", testutil.TestActivationSecret)

	tests := []struct {
		name     string
		req      *pb.ResetPasswordRequest
		setup    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher)
		wantCode codes.Code
	}{
		{
			name: "success",
			req:  &pb.ResetPasswordRequest{Token: validToken, NewPassword: "NewPass123!"},
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("UpdateUserPassword", context.Background(),
					matchUpdatePasswordEmail("reset@test.com")).Return(nil)
				pub.On("Publish", utils.EmailEvent{Type: "CONFIRMATION", Email: "reset@test.com", Token: ""}).
					Return(nil)
			},
			wantCode: codes.OK,
		},
		{
			name:     "password too short",
			req:      &pb.ResetPasswordRequest{Token: validToken, NewPassword: "short"},
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid reset token",
			req:      &pb.ResetPasswordRequest{Token: "bad", NewPassword: "ValidPass1!"},
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			wantCode: codes.Unauthenticated,
		},
		{
			name: "db error updating password",
			req:  &pb.ResetPasswordRequest{Token: validToken, NewPassword: "ValidPass1!"},
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("UpdateUserPassword", context.Background(),
					matchUpdatePasswordEmail("reset@test.com")).Return(errors.New("db error"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			pub := &mocks.MockEmailPublisher{}
			tc.setup(q, pub)
			h := newHandler(q, pub)

			_, err := h.ResetPassword(context.Background(), tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			q.AssertExpectations(t)
			pub.AssertExpectations(t)
		})
	}
}

// ─── CreateEmployee validation paths (no sqlDB needed) ───────────────────────

func TestCreateEmployee_ValidationAndAuth(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		req      *pb.CreateEmployeeRequest
		wantCode codes.Code
	}{
		{
			name:     "non-admin denied",
			ctx:      testutil.EmployeeContext("2", nil),
			req:      &pb.CreateEmployeeRequest{Email: "e@test.com", FirstName: "A", LastName: "B"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "missing email",
			ctx:      testutil.AdminContext(),
			req:      &pb.CreateEmployeeRequest{Email: "", FirstName: "A", LastName: "B"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing first name",
			ctx:      testutil.AdminContext(),
			req:      &pb.CreateEmployeeRequest{Email: "e@test.com", FirstName: "", LastName: "B"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing last name",
			ctx:      testutil.AdminContext(),
			req:      &pb.CreateEmployeeRequest{Email: "e@test.com", FirstName: "A", LastName: ""},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "birth date in the future",
			ctx:  testutil.AdminContext(),
			req: &pb.CreateEmployeeRequest{
				Email:     "e@test.com",
				FirstName: "A",
				LastName:  "B",
				BirthDate: time.Now().Add(24 * time.Hour).UnixMilli(),
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "invalid phone number",
			ctx:  testutil.AdminContext(),
			req: &pb.CreateEmployeeRequest{
				Email:       "e@test.com",
				FirstName:   "A",
				LastName:    "B",
				PhoneNumber: "not-a-phone!",
			},
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(&mocks.MockQuerier{}, &mocks.MockEmailPublisher{})
			_, err := h.CreateEmployee(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
		})
	}
}

// ─── UpdateEmployee validation paths ─────────────────────────────────────────

func TestUpdateEmployee_ValidationAndAuth(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		req      *pb.UpdateEmployeeRequest
		setup    func(q *mocks.MockQuerier)
		wantCode codes.Code
	}{
		{
			name:     "no claims",
			ctx:      testutil.UnauthenticatedContext(),
			req:      &pb.UpdateEmployeeRequest{Id: 1, Email: "x@y.com", FirstName: "A", LastName: "B"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "employee without manage_users",
			ctx:      testutil.EmployeeContext("2", nil),
			req:      &pb.UpdateEmployeeRequest{Id: 1, Email: "x@y.com", FirstName: "A", LastName: "B"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "missing id",
			ctx:      testutil.AdminContext(),
			req:      &pb.UpdateEmployeeRequest{Id: 0, Email: "x@y.com", FirstName: "A", LastName: "B"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing email",
			ctx:      testutil.AdminContext(),
			req:      &pb.UpdateEmployeeRequest{Id: 1, Email: "", FirstName: "A", LastName: "B"},
			setup:    func(q *mocks.MockQuerier) {},
			wantCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			tc.setup(q)
			h := newHandler(q, &mocks.MockEmailPublisher{})
			_, err := h.UpdateEmployee(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			q.AssertExpectations(t)
		})
	}
}

// ─── Argument matchers ────────────────────────────────────────────────────────

// matchUpdatePasswordEmail matches an UpdateUserPasswordParams with any hash
// but a specific email (since bcrypt hash is non-deterministic).
func matchUpdatePasswordEmail(email string) interface{} {
	return mock.MatchedBy(func(p sqlc.UpdateUserPasswordParams) bool {
		return p.Email == email && p.PasswordHash != ""
	})
}

// matchEmailType matches an EmailEvent with a specific Type.
func matchEmailType(evType string) interface{} {
	return mock.MatchedBy(func(e utils.EmailEvent) bool {
		return e.Type == evType
	})
}

// ─── CreateClient ─────────────────────────────────────────────────────────────

func TestCreateClient(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		setup     func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher)
		req       *pb.CreateClientRequest
		wantCode  codes.Code
		checkResp func(t *testing.T, r *pb.CreateClientResponse)
	}{
		{
			name:     "permission denied — caller is ADMIN",
			ctx:      testutil.AdminContext(),
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req:      &pb.CreateClientRequest{Email: "c@test.com", FirstName: "Ana", LastName: "Anić"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "permission denied — unauthenticated",
			ctx:      testutil.UnauthenticatedContext(),
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req:      &pb.CreateClientRequest{Email: "c@test.com", FirstName: "Ana", LastName: "Anić"},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "missing email",
			ctx:      testutil.EmployeeContext("2", []string{}),
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req:      &pb.CreateClientRequest{FirstName: "Ana", LastName: "Anić"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing first name",
			ctx:      testutil.EmployeeContext("2", []string{}),
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req:      &pb.CreateClientRequest{Email: "c@test.com", LastName: "Anić"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "missing last name",
			ctx:      testutil.EmployeeContext("2", []string{}),
			setup:    func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req:      &pb.CreateClientRequest{Email: "c@test.com", FirstName: "Ana"},
			wantCode: codes.InvalidArgument,
		},
		{
			name:  "birth date in the future",
			ctx:   testutil.EmployeeContext("2", []string{}),
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {},
			req: &pb.CreateClientRequest{
				Email:     "c@test.com",
				FirstName: "Ana",
				LastName:  "Anić",
				BirthDate: time.Now().UnixMilli() + 86400000,
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "email already registered",
			ctx:  testutil.EmployeeContext("2", []string{}),
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("CreateUser", mock.Anything, mock.Anything).
					Return(sqlc.CreateUserRow{}, pgDupErr())
			},
			req:      &pb.CreateClientRequest{Email: "taken@test.com", FirstName: "Ana", LastName: "Anić"},
			wantCode: codes.AlreadyExists,
		},
		{
			name: "db error",
			ctx:  testutil.EmployeeContext("2", []string{}),
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("CreateUser", mock.Anything, mock.Anything).
					Return(sqlc.CreateUserRow{}, errors.New("db down"))
			},
			req:      &pb.CreateClientRequest{Email: "c@test.com", FirstName: "Ana", LastName: "Anić"},
			wantCode: codes.Internal,
		},
		{
			name: "success — user_type CLIENT, password empty, activation email sent",
			ctx:  testutil.EmployeeContext("2", []string{}),
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("CreateUser", mock.Anything, mock.MatchedBy(func(p sqlc.CreateUserParams) bool {
					return p.UserType == "CLIENT" &&
						p.Email == "new@test.com" &&
						p.PasswordHash == "" &&
						p.SaltPassword == ""
				})).Return(sqlc.CreateUserRow{ID: 42, Email: "new@test.com"}, nil)
				pub.On("Publish", mock.MatchedBy(func(e utils.EmailEvent) bool {
					return e.Type == "ACTIVATION" && e.Email == "new@test.com" && e.Token != ""
				})).Return(nil)
			},
			req: &pb.CreateClientRequest{
				Email:     "new@test.com",
				FirstName: "Ana",
				LastName:  "Anić",
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.CreateClientResponse) {
				assert.Equal(t, int64(42), r.Id)
				assert.Equal(t, "new@test.com", r.Email)
			},
		},
		{
			name: "success — publish failure is non-fatal",
			ctx:  testutil.EmployeeContext("2", []string{}),
			setup: func(q *mocks.MockQuerier, pub *mocks.MockEmailPublisher) {
				q.On("CreateUser", mock.Anything, mock.Anything).
					Return(sqlc.CreateUserRow{ID: 99, Email: "ok@test.com"}, nil)
				pub.On("Publish", mock.Anything).Return(errors.New("rabbitmq down"))
			},
			req:      &pb.CreateClientRequest{Email: "ok@test.com", FirstName: "Marko", LastName: "Marković"},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.CreateClientResponse) {
				assert.Equal(t, int64(99), r.Id)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &mocks.MockQuerier{}
			pub := &mocks.MockEmailPublisher{}
			tt.setup(q, pub)
			h := newHandler(q, pub)

			resp, err := h.CreateClient(tt.ctx, tt.req)
			assert.Equal(t, tt.wantCode, grpcCode(err))
			if tt.checkResp != nil {
				require.NoError(t, err)
				tt.checkResp(t, resp)
			}
			q.AssertExpectations(t)
			pub.AssertExpectations(t)
		})
	}
}


// ─── GetClientByID ────────────────────────────────────────────────────────────

func TestGetClientByID(t *testing.T) {
	fullClient := &domain.ClientDetail{
		ID:          42,
		FirstName:   "Ana",
		LastName:    "Petrović",
		Email:       "ana@test.com",
		PhoneNumber: "+381601234567",
		Address:     "Knez Mihailova 1",
		DateOfBirth: 946684800000,
		Gender:      "FEMALE",
	}

	tests := []struct {
		name      string
		ctx       context.Context
		req       *pb.GetClientByIDRequest
		setup     func(svc *mocks.MockClientService)
		wantCode  codes.Code
		checkResp func(t *testing.T, r *pb.GetClientByIDResponse)
	}{
		{
			name: "success",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.GetClientByIDRequest{Id: 42},
			setup: func(svc *mocks.MockClientService) {
				svc.On("GetClientByID", mock.Anything, int64(42)).Return(fullClient, nil)
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.GetClientByIDResponse) {
				require.NotNil(t, r.Client)
				assert.Equal(t, int64(42), r.Client.Id)
				assert.Equal(t, "Ana", r.Client.FirstName)
				assert.Equal(t, "Petrović", r.Client.LastName)
				assert.Equal(t, "ana@test.com", r.Client.Email)
				assert.Equal(t, "+381601234567", r.Client.PhoneNumber)
				assert.Equal(t, "Knez Mihailova 1", r.Client.Address)
				assert.Equal(t, int64(946684800000), r.Client.DateOfBirth)
				assert.Equal(t, pb.Gender_GENDER_FEMALE, r.Client.Gender)
			},
		},
		{
			name:     "unauthenticated — no claims",
			ctx:      testutil.UnauthenticatedContext(),
			req:      &pb.GetClientByIDRequest{Id: 42},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "permission denied — admin caller",
			ctx:      testutil.AdminContext(),
			req:      &pb.GetClientByIDRequest{Id: 42},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "invalid argument — zero id",
			ctx:      testutil.EmployeeContext("10", nil),
			req:      &pb.GetClientByIDRequest{Id: 0},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "not found — unknown id",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.GetClientByIDRequest{Id: 999},
			setup: func(svc *mocks.MockClientService) {
				svc.On("GetClientByID", mock.Anything, int64(999)).Return(nil, domain.ErrClientNotFound)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "not found — employee row rejected by service",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.GetClientByIDRequest{Id: 5},
			setup: func(svc *mocks.MockClientService) {
				svc.On("GetClientByID", mock.Anything, int64(5)).Return(nil, domain.ErrClientNotFound)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "internal — unexpected db error",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.GetClientByIDRequest{Id: 1},
			setup: func(svc *mocks.MockClientService) {
				svc.On("GetClientByID", mock.Anything, int64(1)).Return(nil, errors.New("db down"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mocks.MockClientService{}
			tc.setup(svc)
			h := newHandlerWithClientSvc(svc)

			resp, err := h.GetClientByID(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				if tc.checkResp != nil {
					tc.checkResp(t, resp)
				}
			} else {
				assert.Nil(t, resp)
			}
			svc.AssertExpectations(t)
		})
	}
}

// ─── UpdateClient ─────────────────────────────────────────────────────────────

func TestUpdateClient(t *testing.T) {
	updatedClient := &domain.ClientDetail{
		ID:          42,
		FirstName:   "Ana",
		LastName:    "Petrović",
		Email:       "new@test.com",
		PhoneNumber: "+381611111111",
		Address:     "Knez Mihailova 1",
		DateOfBirth: 946684800000,
		Gender:      "FEMALE",
	}

	tests := []struct {
		name      string
		ctx       context.Context
		req       *pb.UpdateClientRequest
		setup     func(svc *mocks.MockClientService)
		wantCode  codes.Code
		checkResp func(t *testing.T, r *pb.UpdateClientResponse)
	}{
		{
			name: "success",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.UpdateClientRequest{Id: 42, Email: "new@test.com", PhoneNumber: "+381611111111"},
			setup: func(svc *mocks.MockClientService) {
				svc.On("UpdateClient", mock.Anything, int64(42), domain.UpdateClientInput{
					Email: "new@test.com", PhoneNumber: "+381611111111",
				}).Return(updatedClient, nil)
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.UpdateClientResponse) {
				require.NotNil(t, r.Client)
				assert.Equal(t, int64(42), r.Client.Id)
				assert.Equal(t, "new@test.com", r.Client.Email)
				assert.Equal(t, "+381611111111", r.Client.PhoneNumber)
				assert.Equal(t, pb.Gender_GENDER_FEMALE, r.Client.Gender)
			},
		},
		{
			name:     "unauthenticated — no claims",
			ctx:      testutil.UnauthenticatedContext(),
			req:      &pb.UpdateClientRequest{Id: 42, FirstName: "X"},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "permission denied — admin caller",
			ctx:      testutil.AdminContext(),
			req:      &pb.UpdateClientRequest{Id: 42, FirstName: "X"},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "invalid argument — zero id",
			ctx:      testutil.EmployeeContext("10", nil),
			req:      &pb.UpdateClientRequest{Id: 0, FirstName: "X"},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name:     "invalid argument — bad phone number",
			ctx:      testutil.EmployeeContext("10", nil),
			req:      &pb.UpdateClientRequest{Id: 42, PhoneNumber: "not-a-phone"},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "not found — unknown id",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.UpdateClientRequest{Id: 999, FirstName: "X"},
			setup: func(svc *mocks.MockClientService) {
				svc.On("UpdateClient", mock.Anything, int64(999), domain.UpdateClientInput{FirstName: "X"}).
					Return(nil, domain.ErrClientNotFound)
			},
			wantCode: codes.NotFound,
		},
		{
			name: "already exists — email taken",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.UpdateClientRequest{Id: 42, Email: "taken@test.com"},
			setup: func(svc *mocks.MockClientService) {
				svc.On("UpdateClient", mock.Anything, int64(42), domain.UpdateClientInput{Email: "taken@test.com"}).
					Return(nil, domain.ErrEmailTaken)
			},
			wantCode: codes.AlreadyExists,
		},
		{
			name: "internal — unexpected db error",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.UpdateClientRequest{Id: 42, FirstName: "X"},
			setup: func(svc *mocks.MockClientService) {
				svc.On("UpdateClient", mock.Anything, int64(42), domain.UpdateClientInput{FirstName: "X"}).
					Return(nil, errors.New("db down"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mocks.MockClientService{}
			tc.setup(svc)
			h := newHandlerWithClientSvc(svc)

			resp, err := h.UpdateClient(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				if tc.checkResp != nil {
					tc.checkResp(t, resp)
				}
			} else {
				assert.Nil(t, resp)
			}
			svc.AssertExpectations(t)
		})
	}
}

// ─── ListClients ──────────────────────────────────────────────────────────────

func TestListClients(t *testing.T) {
	summaries := []domain.ClientSummary{
		{ID: 1, FirstName: "Ana", LastName: "Petrović", Email: "ana@test.com", PhoneNumber: "+381601234567"},
		{ID: 2, FirstName: "Marko", LastName: "Nikolić", Email: "marko@test.com", PhoneNumber: ""},
	}

	tests := []struct {
		name      string
		ctx       context.Context
		req       *pb.ListClientsRequest
		setup     func(svc *mocks.MockClientService)
		wantCode  codes.Code
		checkResp func(t *testing.T, r *pb.ListClientsResponse)
	}{
		{
			name: "success — no filter",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.ListClientsRequest{},
			setup: func(svc *mocks.MockClientService) {
				svc.On("ListClients", mock.Anything, domain.ClientFilter{}).
					Return(summaries, false, nil)
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.ListClientsResponse) {
				require.Len(t, r.Clients, 2)
				assert.False(t, r.HasMore)
				assert.Equal(t, int64(1), r.Clients[0].Id)
				assert.Equal(t, "Ana", r.Clients[0].FirstName)
				assert.Equal(t, "Petrović", r.Clients[0].LastName)
				assert.Equal(t, "ana@test.com", r.Clients[0].Email)
				assert.Equal(t, "+381601234567", r.Clients[0].PhoneNumber)
				assert.Equal(t, int64(2), r.Clients[1].Id)
			},
		},
		{
			name: "success — filter by name, has_more",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.ListClientsRequest{Name: "Petrović", Limit: 1},
			setup: func(svc *mocks.MockClientService) {
				svc.On("ListClients", mock.Anything, domain.ClientFilter{Name: "Petrović", Limit: 1}).
					Return(summaries[:1], true, nil)
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.ListClientsResponse) {
				require.Len(t, r.Clients, 1)
				assert.True(t, r.HasMore)
				assert.Equal(t, "Ana", r.Clients[0].FirstName)
			},
		},
		{
			name: "success — filter by email",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.ListClientsRequest{Email: "@test.com", Limit: 5},
			setup: func(svc *mocks.MockClientService) {
				svc.On("ListClients", mock.Anything, domain.ClientFilter{Email: "@test.com", Limit: 5}).
					Return(summaries, false, nil)
			},
			wantCode: codes.OK,
			checkResp: func(t *testing.T, r *pb.ListClientsResponse) {
				assert.Len(t, r.Clients, 2)
				assert.False(t, r.HasMore)
			},
		},
		{
			name:     "permission denied — unauthenticated",
			ctx:      testutil.UnauthenticatedContext(),
			req:      &pb.ListClientsRequest{},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "permission denied — admin caller",
			ctx:      testutil.AdminContext(),
			req:      &pb.ListClientsRequest{},
			setup:    func(svc *mocks.MockClientService) {},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "internal — db error",
			ctx:  testutil.EmployeeContext("10", nil),
			req:  &pb.ListClientsRequest{},
			setup: func(svc *mocks.MockClientService) {
				svc.On("ListClients", mock.Anything, domain.ClientFilter{}).
					Return(nil, false, errors.New("db down"))
			},
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &mocks.MockClientService{}
			tc.setup(svc)
			h := newHandlerWithClientSvc(svc)

			resp, err := h.ListClients(tc.ctx, tc.req)
			assert.Equal(t, tc.wantCode, grpcCode(err))
			if tc.wantCode == codes.OK {
				require.NoError(t, err)
				if tc.checkResp != nil {
					tc.checkResp(t, resp)
				}
			} else {
				assert.Nil(t, resp)
			}
			svc.AssertExpectations(t)
		})
	}
}
