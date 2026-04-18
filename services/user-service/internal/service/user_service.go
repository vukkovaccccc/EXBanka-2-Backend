// Package service contains application use-case logic.
// Clean Architecture: use-case layer — depends only on domain interfaces.
package service

import (
	"banka-backend/services/user-service/internal/domain"
	"banka-backend/services/user-service/internal/utils"
	auth "banka-backend/shared/auth"
)

// userService implements domain.UserService.
type userService struct {
	repo          domain.UserRepository
	accessSecret  string
	refreshSecret string
}

// NewUserService wires dependencies via constructor injection.
func NewUserService(repo domain.UserRepository, accessSecret, refreshSecret string) domain.UserService {
	return &userService{
		repo:          repo,
		accessSecret:  accessSecret,
		refreshSecret: refreshSecret,
	}
}

// Register hashes the password, persists the user, and returns the created entity.
func (s *userService) Register(name, email, password string) (*domain.User, error) {
	// Check uniqueness
	existing, _ := s.repo.FindByEmail(email)
	if existing != nil {
		return nil, domain.ErrEmailTaken
	}

	hash, err := utils.HashPassword(password)
	if err != nil {
		return nil, err
	}

	user := &domain.User{
		Name:         name,
		Email:        email,
		PasswordHash: hash,
	}
	if err := s.repo.Create(user); err != nil {
		return nil, err
	}
	return user, nil
}

// Login verifies credentials and returns a JWT access/refresh token pair.
func (s *userService) Login(email, password string) (string, string, error) {
	user, err := s.repo.FindByEmail(email)
	if err != nil {
		return "", "", domain.ErrInvalidCredentials
	}

	if err := utils.CheckPassword(password, user.PasswordHash); err != nil {
		return "", "", domain.ErrInvalidCredentials
	}

	access, refresh, err := auth.GenerateTokens(
		user.ID, user.Email, "", nil,
		s.accessSecret, s.refreshSecret,
	)
	if err != nil {
		return "", "", err
	}
	return access, refresh, nil
}

// GetByID fetches a user by their UUID.
func (s *userService) GetByID(id string) (*domain.User, error) {
	return s.repo.FindByID(id)
}
