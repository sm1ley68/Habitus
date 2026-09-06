// partner_webhook_repo.go — подписки партнёра на события и очередь доставок.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type PartnerWebhookRepo struct {
	pool *pgxpool.Pool
}

func NewPartnerWebhookRepo(pool *pgxpool.Pool) *PartnerWebhookRepo {
	return &PartnerWebhookRepo{pool: pool}
}

func (r *PartnerWebhookRepo) Create(ctx context.Context, w domain.PartnerWebhook) (domain.PartnerWebhook, error) {
	var out domain.PartnerWebhook
	err := r.pool.QueryRow(ctx, `
		INSERT INTO partner_webhooks(partner_id, url, secret, events, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, partner_id, url, secret, events, active, created_at`,
		w.PartnerID, w.URL, w.Secret, nonNilStrings(w.Events), w.Active,
	).Scan(&out.ID, &out.PartnerID, &out.URL, &out.Secret, &out.Events, &out.Active, &out.CreatedAt)
	return out, err
}

func (r *PartnerWebhookRepo) List(ctx context.Context, partnerID uuid.UUID) ([]domain.PartnerWebhook, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, partner_id, url, secret, events, active, created_at
		FROM partner_webhooks WHERE partner_id = $1 ORDER BY created_at DESC`, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PartnerWebhook{}
	for rows.Next() {
		var w domain.PartnerWebhook
		if err := rows.Scan(&w.ID, &w.PartnerID, &w.URL, &w.Secret, &w.Events,
			&w.Active, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (r *PartnerWebhookRepo) GetOwned(ctx context.Context, partnerID, id uuid.UUID) (domain.PartnerWebhook, error) {
	var w domain.PartnerWebhook
	err := r.pool.QueryRow(ctx, `
		SELECT id, partner_id, url, secret, events, active, created_at
		FROM partner_webhooks WHERE id = $1 AND partner_id = $2`, id, partnerID,
	).Scan(&w.ID, &w.PartnerID, &w.URL, &w.Secret, &w.Events, &w.Active, &w.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PartnerWebhook{}, ErrNotFound
	}
	return w, err
}

func (r *PartnerWebhookRepo) Delete(ctx context.Context, partnerID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM partner_webhooks WHERE id = $1 AND partner_id = $2`, id, partnerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Subscribers — активные подписки партнёра на конкретное событие. Событие
// ищется в массиве, а не сравнением строк: подписка «на всё» перечисляет
// события явно, wildcard'а у нас нет.
func (r *PartnerWebhookRepo) Subscribers(ctx context.Context, partnerID uuid.UUID,
	event string) ([]domain.PartnerWebhook, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, partner_id, url, secret, events, active, created_at
		FROM partner_webhooks
		WHERE partner_id = $1 AND active AND $2 = ANY(events)`, partnerID, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.PartnerWebhook{}
	for rows.Next() {
		var w domain.PartnerWebhook
		if err := rows.Scan(&w.ID, &w.PartnerID, &w.URL, &w.Secret, &w.Events,
			&w.Active, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// --- очередь доставок ---

func (r *PartnerWebhookRepo) Enqueue(ctx context.Context, webhookID uuid.UUID,
	event string, payload map[string]any) (uuid.UUID, error) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO partner_webhook_deliveries(webhook_id, event, payload)
		VALUES ($1, $2, $3) RETURNING id`, webhookID, event, payloadJSON).Scan(&id)
	return id, err
}

// ClaimDue забирает пачку доставок, которым подошло время. Строки помечаются
// отложенными на время попытки прямо в UPDATE ... RETURNING: две реплики
// шлюза не должны отправить один и тот же вебхук дважды.
func (r *PartnerWebhookRepo) ClaimDue(ctx context.Context, limit int,
	lease time.Duration) ([]domain.WebhookDelivery, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE partner_webhook_deliveries d
		SET next_attempt_at = now() + $2::interval
		FROM partner_webhooks w
		WHERE d.id IN (
		        SELECT id FROM partner_webhook_deliveries
		        WHERE status = 'pending' AND next_attempt_at <= now()
		        ORDER BY next_attempt_at
		        FOR UPDATE SKIP LOCKED
		        LIMIT $1)
		  AND w.id = d.webhook_id
		RETURNING d.id, d.webhook_id, d.event, d.payload, d.status, d.attempts,
		          d.last_error, d.next_attempt_at, d.delivered_at, d.created_at,
		          w.url, w.secret`, limit, lease.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.WebhookDelivery{}
	for rows.Next() {
		var d domain.WebhookDelivery
		var payloadJSON []byte
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &payloadJSON, &d.Status,
			&d.Attempts, &d.LastError, &d.NextAttemptAt, &d.DeliveredAt, &d.CreatedAt,
			&d.URL, &d.Secret); err != nil {
			return nil, err
		}
		if len(payloadJSON) > 0 {
			if err := json.Unmarshal(payloadJSON, &d.Payload); err != nil {
				return nil, err
			}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *PartnerWebhookRepo) MarkDelivered(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE partner_webhook_deliveries
		SET status = 'delivered', delivered_at = now(), attempts = attempts + 1, last_error = ''
		WHERE id = $1`, id)
	return err
}

// MarkFailed откладывает следующую попытку либо окончательно хоронит доставку,
// когда попытки исчерпаны. Причина сохраняется: партнёр должен видеть, почему
// событие до него не дошло, а не только что оно «не дошло».
func (r *PartnerWebhookRepo) MarkFailed(ctx context.Context, id uuid.UUID,
	reason string, retryIn time.Duration, maxAttempts int) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE partner_webhook_deliveries
		SET attempts = attempts + 1,
		    last_error = $2,
		    status = CASE WHEN attempts + 1 >= $4 THEN 'failed' ELSE 'pending' END,
		    next_attempt_at = now() + $3::interval
		WHERE id = $1`, id, reason, retryIn.String(), maxAttempts)
	return err
}

// ListDeliveries — журнал доставок вебхука: партнёр обязан иметь возможность
// увидеть, что мы отправляли и чем это кончилось.
func (r *PartnerWebhookRepo) ListDeliveries(ctx context.Context, webhookID uuid.UUID,
	limit, offset int) ([]domain.WebhookDelivery, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM partner_webhook_deliveries WHERE webhook_id = $1`, webhookID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id, webhook_id, event, payload, status, attempts, last_error,
		       next_attempt_at, delivered_at, created_at
		FROM partner_webhook_deliveries
		WHERE webhook_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`, webhookID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.WebhookDelivery{}
	for rows.Next() {
		var d domain.WebhookDelivery
		var payloadJSON []byte
		if err := rows.Scan(&d.ID, &d.WebhookID, &d.Event, &payloadJSON, &d.Status,
			&d.Attempts, &d.LastError, &d.NextAttemptAt, &d.DeliveredAt,
			&d.CreatedAt); err != nil {
			return nil, 0, err
		}
		if len(payloadJSON) > 0 {
			if err := json.Unmarshal(payloadJSON, &d.Payload); err != nil {
				return nil, 0, err
			}
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}
