package repository

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SessionRepo struct {
	pool *pgxpool.Pool
}

func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo {
	return &SessionRepo{pool: pool}
}

func (r *SessionRepo) Create(ctx context.Context, tokenHash string, userID uuid.UUID, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions(token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, userID, expiresAt)
	return err
}

// GetSession отдаёт владельца сессии вместе с признаком гостя — одним
// запросом, а не двумя: этот вызов стоит на каждом запросе к API, и лишний
// round-trip к БД тут заметен. ErrNotFound на отсутствующей или протухшей.
func (r *SessionRepo) GetSession(ctx context.Context, tokenHash string) (uuid.UUID, bool, error) {
	var userID uuid.UUID
	var isGuest bool
	err := r.pool.QueryRow(ctx, `
		SELECT s.user_id, u.is_guest
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now()`,
		tokenHash,
	).Scan(&userID, &isGuest)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, ErrNotFound
	}
	return userID, isGuest, err
}

func (r *SessionRepo) Delete(ctx context.Context, tokenHash string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// DeleteExpired вычищает протухшие сессии и возвращает число удалённых строк.
// GetSession их и так не отдаёт (фильтр по expires_at), но без чистки таблица
// растёт вечно — за месяц TTL там оседают все логины, что были.
func (r *SessionRepo) DeleteExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
