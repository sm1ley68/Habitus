package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type EventRepo struct {
	pool *pgxpool.Pool
}

func NewEventRepo(pool *pgxpool.Pool) *EventRepo {
	return &EventRepo{pool: pool}
}

func (r *EventRepo) Insert(ctx context.Context, e domain.ProductEvent) error {
	var userID any
	if e.UserID != uuid.Nil {
		userID = e.UserID
	}
	var externalID any
	if e.ExternalID != "" {
		externalID = e.ExternalID
	}
	props := e.Props
	if props == nil {
		props = map[string]any{}
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO product_events(user_id, is_guest, kind, chat_id, external_id, props)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, e.IsGuest, e.Kind, e.ChatID, externalID, props)
	return err
}
