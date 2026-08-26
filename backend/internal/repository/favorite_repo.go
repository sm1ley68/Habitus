package repository

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type FavoriteRepo struct {
	pool *pgxpool.Pool
}

func NewFavoriteRepo(pool *pgxpool.Pool) *FavoriteRepo {
	return &FavoriteRepo{pool: pool}
}

// Add идемпотентен: повторный клик по «сохранить» — то же состояние, а не
// ошибка. chat_id при повторе обновляется: последний контекст сохранения
// полезнее первого.
func (r *FavoriteRepo) Add(ctx context.Context, userID uuid.UUID,
	externalID string, chatID *uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO favorites(user_id, external_id, chat_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, external_id)
		DO UPDATE SET chat_id = COALESCE(EXCLUDED.chat_id, favorites.chat_id)`,
		userID, externalID, chatID)
	return err
}

// Remove тоже идемпотентен: удаление отсутствующего — уже нужное состояние.
func (r *FavoriteRepo) Remove(ctx context.Context, userID uuid.UUID, externalID string) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM favorites WHERE user_id = $1 AND external_id = $2`, userID, externalID)
	return err
}

// List отдаёт избранное, свежее сверху. total считается отдельным COUNT(*),
// а не оконной функцией над той же выборкой: на странице за концом списка
// окно не вернёт ни одной строки, и total ложно схлопнется в 0 (см.
// ChatSearchRepo.ListResults — тот же приём).
func (r *FavoriteRepo) List(ctx context.Context, userID uuid.UUID,
	limit, offset int) ([]domain.Favorite, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM favorites WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT user_id, external_id, chat_id, created_at
		FROM favorites
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]domain.Favorite, 0, limit)
	for rows.Next() {
		var f domain.Favorite
		if err := rows.Scan(&f.UserID, &f.ExternalID, &f.ChatID, &f.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}
