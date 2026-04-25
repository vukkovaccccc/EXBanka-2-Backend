package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"banka-backend/services/bank-service/internal/domain"
	"banka-backend/services/bank-service/internal/service"
)

// ─── Mock ActuaryRepository ───────────────────────────────────────────────────

type mockActuaryRepo struct {
	mock.Mock
}

func (m *mockActuaryRepo) Create(ctx context.Context, input domain.CreateActuaryInput) (*domain.Actuary, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) GetByID(ctx context.Context, id int64) (*domain.Actuary, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) GetByEmployeeID(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
	args := m.Called(ctx, employeeID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) List(ctx context.Context, actuaryType string) ([]domain.Actuary, error) {
	args := m.Called(ctx, actuaryType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) Update(ctx context.Context, input domain.UpdateActuaryInput) (*domain.Actuary, error) {
	args := m.Called(ctx, input)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) Delete(ctx context.Context, id int64) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockActuaryRepo) DeleteByEmployeeID(ctx context.Context, employeeID int64) error {
	return m.Called(ctx, employeeID).Error(0)
}
func (m *mockActuaryRepo) ResetAllUsedLimits(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockActuaryRepo) IncrementUsedLimitIfWithin(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, error) {
	args := m.Called(ctx, employeeID, amount)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actuary), args.Error(1)
}
func (m *mockActuaryRepo) IncrementUsedLimitAlways(ctx context.Context, employeeID int64, amount decimal.Decimal) (*domain.Actuary, bool, error) {
	args := m.Called(ctx, employeeID, amount)
	if args.Get(0) == nil {
		return nil, args.Bool(1), args.Error(2)
	}
	return args.Get(0).(*domain.Actuary), args.Bool(1), args.Error(2)
}
func (m *mockActuaryRepo) InsertActuaryLimitAudit(ctx context.Context, actorEmployeeID, targetEmployeeID int64, oldLimit, newLimit decimal.Decimal) error {
	return m.Called(ctx, actorEmployeeID, targetEmployeeID, oldLimit, newLimit).Error(0)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func newActuaryService(repo domain.ActuaryRepository) domain.ActuaryService {
	return service.NewActuaryService(repo)
}

// ─── GetActuaryByID ───────────────────────────────────────────────────────────

func TestActuaryService_GetActuaryByID_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	expected := &domain.Actuary{ID: 1, EmployeeID: 42}
	repo.On("GetByID", ctx, int64(1)).Return(expected, nil)

	svc := newActuaryService(repo)
	got, err := svc.GetActuaryByID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, expected, got)
	repo.AssertExpectations(t)
}

func TestActuaryService_GetActuaryByID_NotFound(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("GetByID", ctx, int64(99)).Return(nil, domain.ErrActuaryNotFound)

	svc := newActuaryService(repo)
	_, err := svc.GetActuaryByID(ctx, 99)
	assert.ErrorIs(t, err, domain.ErrActuaryNotFound)
}

// ─── GetActuaryByEmployeeID ───────────────────────────────────────────────────

func TestActuaryService_GetActuaryByEmployeeID_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	expected := &domain.Actuary{ID: 2, EmployeeID: 10}
	repo.On("GetByEmployeeID", ctx, int64(10)).Return(expected, nil)

	svc := newActuaryService(repo)
	got, err := svc.GetActuaryByEmployeeID(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestActuaryService_GetActuaryByEmployeeID_NotFound(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("GetByEmployeeID", ctx, int64(5)).Return(nil, domain.ErrActuaryNotFound)

	svc := newActuaryService(repo)
	_, err := svc.GetActuaryByEmployeeID(ctx, 5)
	assert.ErrorIs(t, err, domain.ErrActuaryNotFound)
}

// ─── ListAgents ───────────────────────────────────────────────────────────────

func TestActuaryService_ListAgents_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	agents := []domain.Actuary{{ID: 1}, {ID: 2}}
	repo.On("List", ctx, string(domain.ActuaryTypeAgent)).Return(agents, nil)

	svc := newActuaryService(repo)
	got, err := svc.ListAgents(ctx)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestActuaryService_ListAgents_Error(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("List", ctx, string(domain.ActuaryTypeAgent)).Return(nil, errors.New("db error"))

	svc := newActuaryService(repo)
	_, err := svc.ListAgents(ctx)
	assert.Error(t, err)
}

// ─── SetAgentLimit ────────────────────────────────────────────────────────────

func TestActuaryService_SetAgentLimit_NegativeLimit(t *testing.T) {
	svc := newActuaryService(&mockActuaryRepo{})
	_, err := svc.SetAgentLimit(context.Background(), 1, 2, decimal.NewFromFloat(-10))
	assert.ErrorIs(t, err, domain.ErrActuaryLimitNegative)
}

func TestActuaryService_SetAgentLimit_ZeroLimit(t *testing.T) {
	svc := newActuaryService(&mockActuaryRepo{})
	_, err := svc.SetAgentLimit(context.Background(), 1, 2, decimal.Zero)
	assert.ErrorIs(t, err, domain.ErrActuaryLimitZero)
}

func TestActuaryService_SetAgentLimit_BelowUsedLimit(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	a := &domain.Actuary{
		ID:          3,
		EmployeeID:  7,
		ActuaryType: domain.ActuaryTypeAgent,
		Limit:       decimal.NewFromFloat(100),
		UsedLimit:   decimal.NewFromFloat(80),
	}
	repo.On("GetByEmployeeID", ctx, int64(7)).Return(a, nil)

	svc := newActuaryService(repo)
	// Try to set limit to 50, but used_limit is 80 → error
	_, err := svc.SetAgentLimit(ctx, 1, 7, decimal.NewFromFloat(50))
	assert.ErrorIs(t, err, domain.ErrActuaryLimitBelowUsed)
}

func TestActuaryService_SetAgentLimit_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	a := &domain.Actuary{
		ID:          3,
		EmployeeID:  7,
		ActuaryType: domain.ActuaryTypeAgent,
		Limit:       decimal.NewFromFloat(100),
		UsedLimit:   decimal.NewFromFloat(20),
	}
	updated := &domain.Actuary{ID: 3, Limit: decimal.NewFromFloat(200)}
	repo.On("GetByEmployeeID", ctx, int64(7)).Return(a, nil)
	repo.On("Update", ctx, mock.MatchedBy(func(inp domain.UpdateActuaryInput) bool {
		return inp.ID == 3 && inp.Limit.Equal(decimal.NewFromFloat(200))
	})).Return(updated, nil)
	repo.On("InsertActuaryLimitAudit", ctx, int64(1), int64(7), a.Limit, decimal.NewFromFloat(200)).Return(nil)

	svc := newActuaryService(repo)
	got, err := svc.SetAgentLimit(ctx, 1, 7, decimal.NewFromFloat(200))
	require.NoError(t, err)
	assert.Equal(t, updated, got)
	repo.AssertExpectations(t)
}

func TestActuaryService_SetAgentLimit_EmployeeNotFound(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("GetByEmployeeID", ctx, int64(99)).Return(nil, domain.ErrActuaryNotFound)

	svc := newActuaryService(repo)
	_, err := svc.SetAgentLimit(ctx, 1, 99, decimal.NewFromFloat(100))
	assert.ErrorIs(t, err, domain.ErrActuaryNotFound)
}

// ─── ResetAgentUsedLimit ──────────────────────────────────────────────────────

func TestActuaryService_ResetAgentUsedLimit_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	a := &domain.Actuary{
		ID:           5,
		EmployeeID:   8,
		ActuaryType:  domain.ActuaryTypeAgent,
		Limit:        decimal.NewFromFloat(100),
		UsedLimit:    decimal.NewFromFloat(50),
		NeedApproval: true,
	}
	updated := &domain.Actuary{ID: 5, UsedLimit: decimal.Zero}
	repo.On("GetByEmployeeID", ctx, int64(8)).Return(a, nil)
	repo.On("Update", ctx, mock.MatchedBy(func(inp domain.UpdateActuaryInput) bool {
		return inp.ID == 5 && inp.UsedLimit.Equal(decimal.Zero)
	})).Return(updated, nil)

	svc := newActuaryService(repo)
	got, err := svc.ResetAgentUsedLimit(ctx, 8)
	require.NoError(t, err)
	assert.Equal(t, updated, got)
}

func TestActuaryService_ResetAgentUsedLimit_NotFound(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("GetByEmployeeID", ctx, int64(99)).Return(nil, domain.ErrActuaryNotFound)

	svc := newActuaryService(repo)
	_, err := svc.ResetAgentUsedLimit(ctx, 99)
	assert.ErrorIs(t, err, domain.ErrActuaryNotFound)
}

// ─── SetAgentNeedApproval ─────────────────────────────────────────────────────

func TestActuaryService_SetAgentNeedApproval_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	a := &domain.Actuary{ID: 6, EmployeeID: 9, ActuaryType: domain.ActuaryTypeAgent, NeedApproval: false}
	updated := &domain.Actuary{ID: 6, NeedApproval: true}
	repo.On("GetByEmployeeID", ctx, int64(9)).Return(a, nil)
	repo.On("Update", ctx, mock.MatchedBy(func(inp domain.UpdateActuaryInput) bool {
		return inp.ID == 6 && inp.NeedApproval == true
	})).Return(updated, nil)

	svc := newActuaryService(repo)
	got, err := svc.SetAgentNeedApproval(ctx, 9, true)
	require.NoError(t, err)
	assert.True(t, got.NeedApproval)
}

func TestActuaryService_SetAgentNeedApproval_GetByEmployeeIDError(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("GetByEmployeeID", ctx, int64(10)).Return(nil, errors.New("db error"))

	svc := newActuaryService(repo)
	_, err := svc.SetAgentNeedApproval(ctx, 10, true)
	assert.Error(t, err)
}

// ─── CreateActuaryForEmployee ─────────────────────────────────────────────────

func TestActuaryService_CreateActuaryForEmployee_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	created := &domain.Actuary{ID: 7, EmployeeID: 11, ActuaryType: domain.ActuaryTypeAgent}
	repo.On("Create", ctx, mock.MatchedBy(func(inp domain.CreateActuaryInput) bool {
		return inp.EmployeeID == 11 && inp.ActuaryType == domain.ActuaryTypeAgent && !inp.NeedApproval
	})).Return(created, nil)

	svc := newActuaryService(repo)
	got, err := svc.CreateActuaryForEmployee(ctx, 11, domain.ActuaryTypeAgent)
	require.NoError(t, err)
	assert.Equal(t, created, got)
}

func TestActuaryService_CreateActuaryForEmployee_Supervisor(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	created := &domain.Actuary{ID: 8, EmployeeID: 12, ActuaryType: domain.ActuaryTypeSupervisor}
	repo.On("Create", ctx, mock.MatchedBy(func(inp domain.CreateActuaryInput) bool {
		return inp.EmployeeID == 12 && inp.ActuaryType == domain.ActuaryTypeSupervisor
	})).Return(created, nil)

	svc := newActuaryService(repo)
	got, err := svc.CreateActuaryForEmployee(ctx, 12, domain.ActuaryTypeSupervisor)
	require.NoError(t, err)
	assert.Equal(t, created, got)
}

// ─── DeleteActuaryForEmployee ─────────────────────────────────────────────────

func TestActuaryService_DeleteActuaryForEmployee_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("DeleteByEmployeeID", ctx, int64(15)).Return(nil)

	svc := newActuaryService(repo)
	err := svc.DeleteActuaryForEmployee(ctx, 15)
	assert.NoError(t, err)
}

func TestActuaryService_DeleteActuaryForEmployee_Error(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("DeleteByEmployeeID", ctx, int64(16)).Return(errors.New("db error"))

	svc := newActuaryService(repo)
	err := svc.DeleteActuaryForEmployee(ctx, 16)
	assert.Error(t, err)
}

// ─── ResetAllAgentsUsedLimit ──────────────────────────────────────────────────

func TestActuaryService_ResetAllAgentsUsedLimit_Success(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("ResetAllUsedLimits", ctx).Return(nil)

	svc := newActuaryService(repo)
	err := svc.ResetAllAgentsUsedLimit(ctx)
	assert.NoError(t, err)
}

func TestActuaryService_ResetAllAgentsUsedLimit_Error(t *testing.T) {
	repo := &mockActuaryRepo{}
	ctx := context.Background()
	repo.On("ResetAllUsedLimits", ctx).Return(errors.New("db error"))

	svc := newActuaryService(repo)
	err := svc.ResetAllAgentsUsedLimit(ctx)
	assert.Error(t, err)
}
