package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type ChatRepo struct {
	pool *pgxpool.Pool
}

func NewChatRepo(pool *pgxpool.Pool) *ChatRepo {
	return &ChatRepo{pool: pool}
}

func scanChat(row pgx.Row) (domain.Chat, error) {
	var c domain.Chat
	err := row.Scan(&c.ID, &c.UserID, &c.City, &c.Title, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Chat{}, ErrNotFound
	}
	return c, err
}

func (r *ChatRepo) Create(ctx context.Context, userID uuid.UUID, city, title string) (domain.Chat, error) {
	return scanChat(r.pool.QueryRow(ctx, `
		INSERT INTO chats(user_id, city, title) VALUES ($1, $2, $3)
		RETURNING id, user_id, city, title, created_at, updated_at`,
		userID, city, title))
}

// GetOwned returns ErrNotFound if the chat doesn't exist or isn't owned by userID —
// callers must map that uniformly to 404 chat_not_found, never 403, so a chat's
// existence is never leaked to a non-owner.
func (r *ChatRepo) GetOwned(ctx context.Context, chatID, userID uuid.UUID) (domain.Chat, error) {
	return scanChat(r.pool.QueryRow(ctx, `
		SELECT id, user_id, city, title, created_at, updated_at
		FROM chats WHERE id = $1 AND user_id = $2`, chatID, userID))
}

func (r *ChatRepo) List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Chat, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, city, title, created_at, updated_at
		FROM chats WHERE user_id = $1
		ORDER BY updated_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var chats []domain.Chat
	for rows.Next() {
		c, err := scanChat(rows)
		if err != nil {
			return nil, 0, err
		}
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM chats WHERE user_id = $1`, userID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return chats, total, nil
}

func (r *ChatRepo) Rename(ctx context.Context, chatID uuid.UUID, title string) (domain.Chat, error) {
	return scanChat(r.pool.QueryRow(ctx, `
		UPDATE chats SET title = $1, updated_at = now() WHERE id = $2
		RETURNING id, user_id, city, title, created_at, updated_at`, title, chatID))
}

func (r *ChatRepo) Delete(ctx context.Context, chatID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM chats WHERE id = $1`, chatID)
	return err
}

func (r *ChatRepo) SetStreamActive(ctx context.Context, chatID uuid.UUID, active bool) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE chats SET stream_active = $1,
		       stream_started_at = CASE WHEN $1 THEN now() ELSE stream_started_at END
		WHERE id = $2`, active, chatID)
	return err
}

func (r *ChatRepo) Touch(ctx context.Context, chatID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE chats SET updated_at = now() WHERE id = $1`, chatID)
	return err
}
