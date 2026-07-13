package repository

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type MessageRepo struct {
	pool *pgxpool.Pool
}

func NewMessageRepo(pool *pgxpool.Pool) *MessageRepo {
	return &MessageRepo{pool: pool}
}

func scanMessage(row pgx.Row) (domain.Message, error) {
	var m domain.Message
	var meta []byte
	err := row.Scan(&m.ID, &m.ChatID, &m.Role, &m.Text, &meta, &m.CreatedAt)
	if err != nil {
		return domain.Message{}, err
	}
	if len(meta) > 0 {
		_ = json.Unmarshal(meta, &m.Meta)
	}
	return m, nil
}

func (r *MessageRepo) Insert(ctx context.Context, chatID uuid.UUID, role, text string, meta map[string]any) (domain.Message, error) {
	var metaJSON []byte
	if meta != nil {
		var err error
		metaJSON, err = json.Marshal(meta)
		if err != nil {
			return domain.Message{}, err
		}
	}
	return scanMessage(r.pool.QueryRow(ctx, `
		INSERT INTO messages(chat_id, role, text, meta) VALUES ($1, $2, $3, $4)
		RETURNING id, chat_id, role, text, meta, created_at`,
		chatID, role, text, metaJSON))
}

func (r *MessageRepo) List(ctx context.Context, chatID uuid.UUID, limit, offset int) ([]domain.Message, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, chat_id, role, text, meta, created_at
		FROM messages WHERE chat_id = $1
		ORDER BY created_at ASC
		LIMIT $2 OFFSET $3`, chatID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var messages []domain.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, 0, err
		}
		messages = append(messages, m)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM messages WHERE chat_id = $1`, chatID).Scan(&total); err != nil {
		return nil, 0, err
	}
	return messages, total, nil
}

func (r *MessageRepo) CountByRole(ctx context.Context, chatID uuid.UUID, role string) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM messages WHERE chat_id = $1 AND role = $2`, chatID, role).Scan(&n)
	return n, err
}
