// Package repository contains GORM-backed implementations of domain repositories.
// Clean Architecture: infrastructure / data layer.
package repository

import (
	"banka-backend/services/user-service/internal/domain"
	"errors"
	"time"

	"gorm.io/gorm"
)

// userModel is the GORM model — kept separate from the domain entity so that
// infrastructure concerns (gorm tags, indexes) don't leak into the domain.
type userModel struct {
	ID           string `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	Name         string `gorm:"not null"`
	Email        string `gorm:"uniqueIndex;not null"`
	PasswordHash string `gorm:"not null"`
	CreatedAt    time.Time
}

func (userModel) TableName() string { return "users" }

// GORMUserRepository implements domain.UserRepository using GORM.
type GORMUserRepository struct {
	db *gorm.DB
}

// NewGORMUserRepository returns a ready-to-use repository.
func NewGORMUserRepository(db *gorm.DB) domain.UserRepository {
	return &GORMUserRepository{db: db}
}

// AutoMigrateModel creates / updates the users table via GORM.
// Prefer SQL migrations in migrations/ for production.
func AutoMigrateModel(db *gorm.DB) error {
	return db.AutoMigrate(&userModel{})
}

// ─── domain.UserRepository implementation ────────────────────────────────────

func (r *GORMUserRepository) Create(u *domain.User) error {
	m := toModel(u)
	if err := r.db.Create(m).Error; err != nil {
		return err
	}
	u.ID = m.ID
	return nil
}

func (r *GORMUserRepository) FindByID(id string) (*domain.User, error) {
	var m userModel
	err := r.db.First(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrUserNotFound
	}
	return toDomain(&m), err
}

func (r *GORMUserRepository) FindByEmail(email string) (*domain.User, error) {
	var m userModel
	err := r.db.First(&m, "email = ?", email).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrUserNotFound
	}
	return toDomain(&m), err
}

func (r *GORMUserRepository) Update(u *domain.User) error {
	return r.db.Save(toModel(u)).Error
}

func (r *GORMUserRepository) Delete(id string) error {
	return r.db.Delete(&userModel{}, "id = ?", id).Error
}

// ─── mapping helpers ──────────────────────────────────────────────────────────

func toModel(u *domain.User) *userModel {
	return &userModel{
		ID:           u.ID,
		Name:         u.Name,
		Email:        u.Email,
		PasswordHash: u.PasswordHash,
	}
}

func toDomain(m *userModel) *domain.User {
	return &domain.User{
		ID:           m.ID,
		Name:         m.Name,
		Email:        m.Email,
		PasswordHash: m.PasswordHash,
	}
}
