package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type FeedbackRepo struct {
	pool *pgxpool.Pool
}

func NewFeedbackRepo(pool *pgxpool.Pool) *FeedbackRepo {
	return &FeedbackRepo{pool: pool}
}

// Upsert: оценку можно передумать. Причина перезаписывается вместе с
// вердиктом — иначе к «подходит» прилипло бы объяснение прошлого «не подходит».
func (r *FeedbackRepo) Upsert(ctx context.Context, f domain.ResultFeedback) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO result_feedback(user_id, chat_id, external_id, verdict, reason)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (user_id, chat_id, external_id)
		DO UPDATE SET verdict = EXCLUDED.verdict,
		              reason = EXCLUDED.reason,
		              updated_at = now()`,
		f.UserID, f.ChatID, f.ExternalID, f.Verdict, f.Reason)
	return err
}
