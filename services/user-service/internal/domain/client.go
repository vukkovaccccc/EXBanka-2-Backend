// Package domain contains pure business entities and interfaces.
// Clean Architecture: innermost layer — zero external dependencies.
package domain

import "context"

// ClientDetail is the full client profile returned to the employee portal.
// Fields mirror the proto ClientDetail message — no infrastructure deps.
type ClientDetail struct {
	ID          int64
	FirstName   string
	LastName    string
	Email       string
	PhoneNumber string
	Address     string
	DateOfBirth int64  // Unix timestamp (ms) — matches DB BIGINT
	Gender      string // "MALE" | "FEMALE" | "OTHER" | ""
}

// ClientSummary is the per-row projection returned by the ListClients use case.
// Includes phone_number; omits sensitive fields (password, JMBG, gender, birth_date).
type ClientSummary struct {
	ID          int64
	FirstName   string
	LastName    string
	Email       string
	PhoneNumber string
}

// ClientFilter carries the optional search criteria and pagination parameters
// for the ListClients use case.
type ClientFilter struct {
	Name   string // partial match on first_name, last_name, or "first last"; "" = no filter
	Email  string // partial match on email; "" = no filter
	Limit  int32  // 0 → service default (20)
	Offset int32
}

// UpdateClientInput carries the fields that may be changed via UpdateClient.
// An empty string means "keep the existing value" — only non-empty fields are applied.
// Password and JMBG are intentionally absent.
type UpdateClientInput struct {
	FirstName   string
	LastName    string
	Email       string
	PhoneNumber string
	Address     string
}

// ClientService defines the use-case contract for client-facing operations.
// The concrete implementation lives in internal/service.
type ClientService interface {
	GetClientByID(ctx context.Context, id int64) (*ClientDetail, error)
	UpdateClient(ctx context.Context, id int64, input UpdateClientInput) (*ClientDetail, error)
	// ListClients returns a page of clients matching the optional name/email
	// filters, sorted alphabetically by last name.
	// The bool return value is true when additional rows exist beyond the page.
	ListClients(ctx context.Context, filter ClientFilter) ([]ClientSummary, bool, error)
}
