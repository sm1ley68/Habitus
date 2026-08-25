package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

var ErrDuplicateEmail = errors.New("email already registered")
var ErrNotFound = errors.New("not found")

type UserRepo struct {
	pool *pgxpool.Pool
}

func NewUserRepo(pool *pgxpool.Pool) *UserRepo {
	return &UserRepo{pool: pool}
}

func (r *UserRepo) Create(ctx context.Context, email, passwordHash, name string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users(email, password_hash, name)
		VALUES ($1, $2, $3)
		RETURNING id, COALESCE(email, ''), COALESCE(password_hash, ''), COALESCE(name, ''), is_guest, created_at, updated_at`,
		email, passwordHash, name,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.User{}, ErrDuplicateEmail
		}
		return domain.User{}, err
	}
	return u, nil
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		SELECT id, COALESCE(email, ''), COALESCE(password_hash, ''), COALESCE(name, ''), is_guest, created_at, updated_at
		FROM users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return u, err
}

func (r *UserRepo) GetByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		SELECT id, COALESCE(email, ''), COALESCE(password_hash, ''), COALESCE(name, ''), is_guest, created_at, updated_at
		FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	return u, err
}

// CreateGuest заводит пользователя без учётных данных — под первый поиск без
// регистрации. Имя ставим сразу, чтобы /me не отдавал пустую строку.
func (r *UserRepo) CreateGuest(ctx context.Context) (domain.User, error) {
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		INSERT INTO users(name, is_guest) VALUES ('Гость', true)
		RETURNING id, COALESCE(email, ''), COALESCE(password_hash, ''),
		          COALESCE(name, ''), is_guest, created_at, updated_at`,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// UpgradeGuest превращает гостя в зарегистрированного, СОХРАНЯЯ id: всё, что
// он успел сделать до регистрации, остаётся при нём. Условие is_guest в WHERE
// защищает от перезаписи настоящего аккаунта — оттуда ErrNotFound.
func (r *UserRepo) UpgradeGuest(ctx context.Context, id uuid.UUID,
	email, passwordHash, name string) (domain.User, error) {
	if name == "" {
		name = "Гость"
	}
	var u domain.User
	err := r.pool.QueryRow(ctx, `
		UPDATE users
		SET email = $2, password_hash = $3, name = $4,
		    is_guest = false, updated_at = now()
		WHERE id = $1 AND is_guest
		RETURNING id, COALESCE(email, ''), COALESCE(password_hash, ''),
		          COALESCE(name, ''), is_guest, created_at, updated_at`,
		id, email, passwordHash, name,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.IsGuest, &u.CreatedAt, &u.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, ErrNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.User{}, ErrDuplicateEmail
		}
		return domain.User{}, err
	}
	return u, nil
}

// DeleteStaleGuests убирает брошенных гостей старше olderThan, у которых не
// осталось живой сессии. Без чистки таблица растёт на каждого посетителя, а
// вместе с ней — чаты и результаты по каскаду. Гость с живой сессией не
// трогается, даже если он старше срока: он прямо сейчас в продукте.
func (r *UserRepo) DeleteStaleGuests(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM users u
		WHERE u.is_guest
		  AND u.created_at < $1
		  AND NOT EXISTS (
		      SELECT 1 FROM sessions s
		      WHERE s.user_id = u.id AND s.expires_at > now())`, cutoff)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
