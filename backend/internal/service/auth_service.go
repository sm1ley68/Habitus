package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

const sessionTTL = 30 * 24 * time.Hour

type AuthService struct {
	users    *repository.UserRepo
	sessions *repository.SessionRepo
}

func NewAuthService(users *repository.UserRepo, sessions *repository.SessionRepo) *AuthService {
	return &AuthService{users: users, sessions: sessions}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newOpaqueToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *AuthService) createSession(ctx context.Context, userID uuid.UUID) (token string, expiresAt time.Time, err error) {
	token, err = newOpaqueToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(sessionTTL)
	if err = s.sessions.Create(ctx, hashToken(token), userID, expiresAt); err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (s *AuthService) Register(ctx context.Context, email, password, name string) (domain.User, string, time.Time, error) {
	if len(password) < 8 {
		return domain.User{}, "", time.Time{}, apperr.Validation("пароль должен быть не короче 8 символов")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	u, err := s.users.Create(ctx, email, string(hash), name)
	if errors.Is(err, repository.ErrDuplicateEmail) {
		return domain.User{}, "", time.Time{}, apperr.Validation("email уже зарегистрирован")
	}
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	token, expiresAt, err := s.createSession(ctx, u.ID)
	return u, token, expiresAt, err
}

func (s *AuthService) Login(ctx context.Context, email, password string) (domain.User, string, time.Time, error) {
	u, err := s.users.GetByEmail(ctx, email)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.User{}, "", time.Time{}, apperr.New(401, "unauthorized", "неверный email или пароль")
	}
	if err != nil {
		return domain.User{}, "", time.Time{}, err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		return domain.User{}, "", time.Time{}, apperr.New(401, "unauthorized", "неверный email или пароль")
	}
	token, expiresAt, err := s.createSession(ctx, u.ID)
	return u, token, expiresAt, err
}

func (s *AuthService) Logout(ctx context.Context, token string) error {
	return s.sessions.Delete(ctx, hashToken(token))
}

func (s *AuthService) Authenticate(ctx context.Context, token string) (uuid.UUID, error) {
	userID, err := s.sessions.GetUserID(ctx, hashToken(token))
	if errors.Is(err, repository.ErrNotFound) {
		return uuid.Nil, apperr.Unauthorized()
	}
	return userID, err
}

func (s *AuthService) GetUser(ctx context.Context, userID uuid.UUID) (domain.User, error) {
	return s.users.GetByID(ctx, userID)
}
