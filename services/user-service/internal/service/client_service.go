// Package service contains application use-case logic.
// Clean Architecture: use-case layer — depends only on domain interfaces and
// the sqlc Querier (which acts as the repository in this service's architecture).
package service

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	db "banka-backend/services/user-service/internal/database/sqlc"
	"banka-backend/services/user-service/internal/domain"
)

// drzavaSystemEmail je sistemski nalog preko koga se knjiži porez na kapitalnu
// dobit. Isključuje se iz svih biranja/listinga namenjenih zaposlenima (picker-a
// za otvaranje računa, kartica, kredita). Sve provere koriste case-insensitive
// match preko strings.EqualFold.
const drzavaSystemEmail = "drzava@exbanka.rs"

// isSystemClient vraća true za sistemske naloge koji ne smeju da se pojave u
// listama klijenata koje vidi zaposleni.
func isSystemClient(email string) bool {
	return strings.EqualFold(strings.TrimSpace(email), drzavaSystemEmail)
}

// clientService implements domain.ClientService.
// The sqlc Querier is injected as the data-access (repository) layer.
type clientService struct {
	querier db.Querier
}

// NewClientService wires the sqlc Querier as the repository.
// Inject mocks.MockQuerier in tests to isolate business logic from the DB.
func NewClientService(querier db.Querier) domain.ClientService {
	return &clientService{querier: querier}
}

// GetClientByID fetches a user by ID, verifies they carry user_type = 'CLIENT',
// and returns the full client profile as a domain entity.
//
// Returns ErrClientNotFound when:
//   - The ID does not exist in the users table.
//   - The row exists but user_type is not 'CLIENT' (prevents employee/admin leakage).
//
// Returns a raw error for unexpected database failures (caller maps to codes.Internal).
func (s *clientService) GetClientByID(ctx context.Context, id int64) (*domain.ClientDetail, error) {
	row, err := s.querier.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrClientNotFound
		}
		return nil, err
	}

	// Guard: only expose CLIENT rows through this use case.
	if row.UserType != "CLIENT" {
		return nil, domain.ErrClientNotFound
	}

	return &domain.ClientDetail{
		ID:          row.ID,
		FirstName:   row.FirstName,
		LastName:    row.LastName,
		Email:       row.Email,
		PhoneNumber: nullStrVal(row.PhoneNumber),
		Address:     nullStrVal(row.Address),
		DateOfBirth: row.BirthDate,
		Gender:      nullStrVal(row.Gender),
	}, nil
}

// UpdateClient applies a partial update to a client's mutable fields.
//
// Fields set to "" in input are left unchanged (keep-existing semantics).
// Password and JMBG are never touched.
//
// Returns ErrClientNotFound when the ID does not exist or the row is not a CLIENT.
// Returns ErrEmailTaken when the new email conflicts with an existing account.
// Returns a raw error for unexpected database failures.
func (s *clientService) UpdateClient(ctx context.Context, id int64, input domain.UpdateClientInput) (*domain.ClientDetail, error) {
	// ── 1. Fetch existing client ──────────────────────────────────────────────
	row, err := s.querier.GetUserByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrClientNotFound
		}
		return nil, err
	}
	if row.UserType != "CLIENT" {
		return nil, domain.ErrClientNotFound
	}

	// ── 2. Merge: apply only non-empty input fields ───────────────────────────
	email := row.Email
	if input.Email != "" {
		email = input.Email
	}
	firstName := row.FirstName
	if input.FirstName != "" {
		firstName = input.FirstName
	}
	lastName := row.LastName
	if input.LastName != "" {
		lastName = input.LastName
	}
	phoneNumber := nullStrVal(row.PhoneNumber)
	if input.PhoneNumber != "" {
		phoneNumber = input.PhoneNumber
	}
	address := nullStrVal(row.Address)
	if input.Address != "" {
		address = input.Address
	}

	// ── 3. Persist (password_hash / salt_password never touched) ─────────────
	err = s.querier.UpdateUser(ctx, db.UpdateUserParams{
		ID:          id,
		Email:       email,
		FirstName:   firstName,
		LastName:    lastName,
		BirthDate:   row.BirthDate,
		Gender:      row.Gender,
		PhoneNumber: sql.NullString{String: phoneNumber, Valid: phoneNumber != ""},
		Address:     sql.NullString{String: address, Valid: address != ""},
		IsActive:    row.IsActive,
	})
	if err != nil {
		if isUniqueViolation(err) {
			return nil, domain.ErrEmailTaken
		}
		return nil, err
	}

	// ── 4. Return updated profile (constructed from merged state) ─────────────
	return &domain.ClientDetail{
		ID:          id,
		FirstName:   firstName,
		LastName:    lastName,
		Email:       email,
		PhoneNumber: phoneNumber,
		Address:     address,
		DateOfBirth: row.BirthDate,
		Gender:      nullStrVal(row.Gender),
	}, nil
}

// ListClients returns a page of clients matching the optional name/email filters,
// sorted by last_name ASC, first_name ASC.
//
// filter.Limit defaults to 20 when zero.
// The bool return is true when rows beyond the current page exist (has_more).
func (s *clientService) ListClients(ctx context.Context, filter domain.ClientFilter) ([]domain.ClientSummary, bool, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 20
	}

	params := db.ListClientsParams{
		Limit:  limit + 1, // fetch one extra to detect has_more
		Offset: filter.Offset,
	}
	if filter.Name != "" {
		params.Name = sql.NullString{String: filter.Name, Valid: true}
	}
	if filter.Email != "" {
		params.Email = sql.NullString{String: filter.Email, Valid: true}
	}

	rows, err := s.querier.ListClients(ctx, params)
	if err != nil {
		return nil, false, err
	}

	hasMore := int32(len(rows)) > limit
	if hasMore {
		rows = rows[:limit]
	}

	summaries := make([]domain.ClientSummary, 0, len(rows))
	for _, r := range rows {
		// Sistemski nalog države ne sme da se pojavi u picker-ima
		// za otvaranje računa, kartice ili odobravanje kredita.
		if isSystemClient(r.Email) {
			continue
		}
		summaries = append(summaries, domain.ClientSummary{
			ID:          r.ID,
			FirstName:   r.FirstName,
			LastName:    r.LastName,
			Email:       r.Email,
			PhoneNumber: nullStrVal(r.PhoneNumber),
		})
	}
	return summaries, hasMore, nil
}

// isUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// nullStrVal extracts the string value from a sql.NullString, returning "" when NULL.
func nullStrVal(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}
