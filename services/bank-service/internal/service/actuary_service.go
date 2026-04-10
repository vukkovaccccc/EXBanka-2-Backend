package service

import (
	"context"

	"banka-backend/services/bank-service/internal/domain"

	"github.com/shopspring/decimal"
)

// =============================================================================
// actuaryService implementira domain.ActuaryService.
// Sloj je namerno tanak: validacija biznis-pravila i orkestracija repozitorijuma.
// =============================================================================

type actuaryService struct {
	repo domain.ActuaryRepository
}

func NewActuaryService(repo domain.ActuaryRepository) domain.ActuaryService {
	return &actuaryService{repo: repo}
}

// ─── Opšte operacije ──────────────────────────────────────────────────────────

func (s *actuaryService) GetActuaryByID(ctx context.Context, id int64) (*domain.Actuary, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *actuaryService) GetActuaryByEmployeeID(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
	return s.repo.GetByEmployeeID(ctx, employeeID)
}

// ─── Operacije supervizorskog portala ─────────────────────────────────────────

// ListAgents vraća sve agente (bez supervizora).
func (s *actuaryService) ListAgents(ctx context.Context) ([]domain.Actuary, error) {
	return s.repo.List(ctx, string(domain.ActuaryTypeAgent))
}

// SetAgentLimit ažurira dnevni limit troškova za datog agenta.
// Validira da novi limit nije manji od trenutnog used_limit (Scenario 3).
// Posle uspešnog ažuriranja upisuje red u actuary_limit_audit.
func (s *actuaryService) SetAgentLimit(ctx context.Context, actorEmployeeID, targetEmployeeID int64, limit decimal.Decimal) (*domain.Actuary, error) {
	if limit.IsNegative() {
		return nil, domain.ErrActuaryLimitNegative
	}
	if limit.IsZero() {
		return nil, domain.ErrActuaryLimitZero
	}
	a, err := s.repo.GetByEmployeeID(ctx, targetEmployeeID)
	if err != nil {
		return nil, err
	}
	oldLimit := a.Limit
	if limit.LessThan(a.UsedLimit) {
		return nil, domain.ErrActuaryLimitBelowUsed
	}
	updated, err := s.repo.Update(ctx, domain.UpdateActuaryInput{
		ID:           a.ID,
		ActuaryType:  a.ActuaryType,
		Limit:        limit,
		UsedLimit:    a.UsedLimit,
		NeedApproval: a.NeedApproval,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.InsertActuaryLimitAudit(ctx, actorEmployeeID, targetEmployeeID, oldLimit, limit); err != nil {
		return nil, err
	}
	return updated, nil
}

// ResetAgentUsedLimit resetuje potrošnju agenta na 0 (ručni reset).
// Automatski reset se izvodi u 23:59 kroz scheduler.
func (s *actuaryService) ResetAgentUsedLimit(ctx context.Context, employeeID int64) (*domain.Actuary, error) {
	a, err := s.repo.GetByEmployeeID(ctx, employeeID)
	if err != nil {
		return nil, err
	}
	return s.repo.Update(ctx, domain.UpdateActuaryInput{
		ID:           a.ID,
		ActuaryType:  a.ActuaryType,
		Limit:        a.Limit,
		UsedLimit:    decimal.Zero,
		NeedApproval: a.NeedApproval,
	})
}

// SetAgentNeedApproval menja flag koji zahteva odobrenje supervizora za svaki order agenta.
func (s *actuaryService) SetAgentNeedApproval(ctx context.Context, employeeID int64, needApproval bool) (*domain.Actuary, error) {
	a, err := s.repo.GetByEmployeeID(ctx, employeeID)
	if err != nil {
		return nil, err
	}
	return s.repo.Update(ctx, domain.UpdateActuaryInput{
		ID:           a.ID,
		ActuaryType:  a.ActuaryType,
		Limit:        a.Limit,
		UsedLimit:    a.UsedLimit,
		NeedApproval: needApproval,
	})
}

// ─── Interne operacije ────────────────────────────────────────────────────────

// CreateActuaryForEmployee kreira actuary_info zapis za zaposlenog koji je dobio SUPERVISOR/AGENT.
// Supervizori se kreiraju sa nultim limitima i bez zahteva za odobrenje.
func (s *actuaryService) CreateActuaryForEmployee(ctx context.Context, employeeID int64, actuaryType domain.ActuaryType) (*domain.Actuary, error) {
	return s.repo.Create(ctx, domain.CreateActuaryInput{
		EmployeeID:   employeeID,
		ActuaryType:  actuaryType,
		Limit:        decimal.Zero,
		UsedLimit:    decimal.Zero,
		NeedApproval: false,
	})
}

// DeleteActuaryForEmployee briše actuary_info zapis za zaposlenog koji je izgubio SUPERVISOR/AGENT.
func (s *actuaryService) DeleteActuaryForEmployee(ctx context.Context, employeeID int64) error {
	return s.repo.DeleteByEmployeeID(ctx, employeeID)
}

// ResetAllAgentsUsedLimit atomski resetuje used_limit na 0 za sve agente.
// Poziva se svake noći u 23:59 kroz DailyLimitResetWorker.
func (s *actuaryService) ResetAllAgentsUsedLimit(ctx context.Context) error {
	return s.repo.ResetAllUsedLimits(ctx)
}
