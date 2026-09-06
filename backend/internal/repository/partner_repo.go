// partner_repo.go — партнёры, ключи API и идемпотентность Partner API.
package repository

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

// ErrIdempotencyMismatch — тот же Idempotency-Key пришёл с другим телом.
// Это ошибка клиента, а не повтор: возвращать ему чужой ответ нельзя.
var ErrIdempotencyMismatch = errors.New("idempotency key reused with a different payload")

// ErrSlugTaken — партнёр с таким slug уже заведён.
var ErrSlugTaken = errors.New("partner slug already taken")

type PartnerRepo struct {
	pool *pgxpool.Pool
}

func NewPartnerRepo(pool *pgxpool.Pool) *PartnerRepo {
	return &PartnerRepo{pool: pool}
}

const partnerColumns = `p.id, p.slug, p.name, p.user_id, p.status,
	p.rate_limit_per_min, p.llm_limit_per_hour, p.created_at, p.updated_at`

func scanPartner(row pgx.Row) (domain.Partner, error) {
	var p domain.Partner
	err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.UserID, &p.Status,
		&p.RateLimitPerMin, &p.LLMPerHour, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Partner{}, ErrNotFound
	}
	return p, err
}

func (r *PartnerRepo) Create(ctx context.Context, p domain.Partner) (domain.Partner, error) {
	out, err := scanPartner(r.pool.QueryRow(ctx, `
		INSERT INTO partners(slug, name, user_id, status, rate_limit_per_min, llm_limit_per_hour)
		-- Пустой статус означает «вызывающий не выбирал» — тогда действует
		-- pending, как и у колонки по умолчанию. Пропустить пустую строку
		-- дальше значило бы упереться в CHECK там, где имелось в виду
		-- «заводим как обычно».
		VALUES ($1, $2, $3, COALESCE(NULLIF($4, ''), 'pending'), $5, $6)
		RETURNING id, slug, name, user_id, status,
		          rate_limit_per_min, llm_limit_per_hour, created_at, updated_at`,
		p.Slug, p.Name, p.UserID, p.Status, p.RateLimitPerMin, p.LLMPerHour))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.Partner{}, ErrSlugTaken
	}
	return out, err
}

func (r *PartnerRepo) GetBySlug(ctx context.Context, slug string) (domain.Partner, error) {
	return scanPartner(r.pool.QueryRow(ctx, `
		SELECT `+partnerColumns+` FROM partners p WHERE p.slug = $1`, slug))
}

func (r *PartnerRepo) List(ctx context.Context) ([]domain.Partner, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+partnerColumns+` FROM partners p ORDER BY p.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Partner{}
	for rows.Next() {
		var p domain.Partner
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.UserID, &p.Status,
			&p.RateLimitPerMin, &p.LLMPerHour, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *PartnerRepo) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE partners SET status = $2, updated_at = now() WHERE id = $1`, id, status)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- ключи ---

func (r *PartnerRepo) CreateKey(ctx context.Context, k domain.APIKey) (domain.APIKey, error) {
	var out domain.APIKey
	err := r.pool.QueryRow(ctx, `
		INSERT INTO partner_api_keys(partner_id, name, prefix, secret_hash, environment,
		                             scopes, allowed_ips, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, partner_id, name, prefix, secret_hash, environment, scopes,
		          allowed_ips, revoked_at, expires_at, last_used_at, created_at`,
		k.PartnerID, k.Name, k.Prefix, k.SecretHash, k.Environment, k.Scopes,
		nonNilStrings(k.AllowedIPs), k.ExpiresAt,
	).Scan(&out.ID, &out.PartnerID, &out.Name, &out.Prefix, &out.SecretHash,
		&out.Environment, &out.Scopes, &out.AllowedIPs, &out.RevokedAt, &out.ExpiresAt,
		&out.LastUsedAt, &out.CreatedAt)
	return out, err
}

// GetKeyWithPartner ищет ключ по открытому префиксу и сразу отдаёт партнёра:
// на каждом запросе Partner API нужны оба, и второй поход в базу тут лишний.
// Сравнение секрета — забота вызывающего: репозиторий не знает про хеши.
func (r *PartnerRepo) GetKeyWithPartner(ctx context.Context, prefix string) (domain.APIKey, domain.Partner, error) {
	var k domain.APIKey
	var p domain.Partner
	err := r.pool.QueryRow(ctx, `
		SELECT k.id, k.partner_id, k.name, k.prefix, k.secret_hash, k.environment,
		       k.scopes, k.allowed_ips, k.revoked_at, k.expires_at, k.last_used_at,
		       k.created_at, `+partnerColumns+`
		FROM partner_api_keys k
		JOIN partners p ON p.id = k.partner_id
		WHERE k.prefix = $1`, prefix,
	).Scan(&k.ID, &k.PartnerID, &k.Name, &k.Prefix, &k.SecretHash, &k.Environment,
		&k.Scopes, &k.AllowedIPs, &k.RevokedAt, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt,
		&p.ID, &p.Slug, &p.Name, &p.UserID, &p.Status,
		&p.RateLimitPerMin, &p.LLMPerHour, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.APIKey{}, domain.Partner{}, ErrNotFound
	}
	return k, p, err
}

func (r *PartnerRepo) ListKeys(ctx context.Context, partnerID uuid.UUID) ([]domain.APIKey, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, partner_id, name, prefix, secret_hash, environment, scopes,
		       allowed_ips, revoked_at, expires_at, last_used_at, created_at
		FROM partner_api_keys WHERE partner_id = $1 ORDER BY created_at DESC`, partnerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.APIKey{}
	for rows.Next() {
		var k domain.APIKey
		if err := rows.Scan(&k.ID, &k.PartnerID, &k.Name, &k.Prefix, &k.SecretHash,
			&k.Environment, &k.Scopes, &k.AllowedIPs, &k.RevokedAt, &k.ExpiresAt,
			&k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *PartnerRepo) RevokeKey(ctx context.Context, prefix string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE partner_api_keys SET revoked_at = now()
		WHERE prefix = $1 AND revoked_at IS NULL`, prefix)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// TouchKey отмечает момент последнего использования. Ошибка здесь не должна
// ронять запрос: это телеметрия ключа, а не часть авторизации, — поэтому
// вызывающий её игнорирует, а сам UPDATE идёт вне транзакции запроса.
func (r *PartnerRepo) TouchKey(ctx context.Context, id uuid.UUID, at time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE partner_api_keys SET last_used_at = $2 WHERE id = $1`, id, at)
	return err
}

// --- идемпотентность ---

// GetIdempotent возвращает сохранённый ответ на тот же ключ. Несовпадение
// отпечатка запроса — ErrIdempotencyMismatch: клиент переиспользовал ключ.
func (r *PartnerRepo) GetIdempotent(ctx context.Context, partnerID uuid.UUID,
	key, requestHash string) (domain.IdempotencyRecord, error) {
	var rec domain.IdempotencyRecord
	err := r.pool.QueryRow(ctx, `
		SELECT partner_id, key, endpoint, request_hash, status_code, response, created_at
		FROM partner_idempotency WHERE partner_id = $1 AND key = $2`, partnerID, key,
	).Scan(&rec.PartnerID, &rec.Key, &rec.Endpoint, &rec.RequestHash,
		&rec.StatusCode, &rec.Response, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IdempotencyRecord{}, ErrNotFound
	}
	if err != nil {
		return domain.IdempotencyRecord{}, err
	}
	if rec.RequestHash != requestHash {
		return domain.IdempotencyRecord{}, ErrIdempotencyMismatch
	}
	return rec, nil
}

func (r *PartnerRepo) SaveIdempotent(ctx context.Context, rec domain.IdempotencyRecord) error {
	// DO NOTHING, а не DO UPDATE: первый успевший ответ и есть канонический,
	// перезапись сделала бы «тот же ключ — тот же ответ» неправдой.
	_, err := r.pool.Exec(ctx, `
		INSERT INTO partner_idempotency(partner_id, key, endpoint, request_hash, status_code, response)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (partner_id, key) DO NOTHING`,
		rec.PartnerID, rec.Key, rec.Endpoint, rec.RequestHash, rec.StatusCode, json.RawMessage(rec.Response))
	return err
}

// SweepIdempotency чистит записи старше ttl. Ключ, которому сутки, повторно
// уже не придёт: клиенты повторяют запрос секундами, а не днями.
func (r *PartnerRepo) SweepIdempotency(ctx context.Context, ttl time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM partner_idempotency WHERE created_at < now() - $1::interval`,
		ttl.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetByUserID — партнёр по служебному аккаунту. Нужен доставке событий:
// заявка знает только seller_id, а вебхуки живут на партнёре.
func (r *PartnerRepo) GetByUserID(ctx context.Context, userID uuid.UUID) (domain.Partner, error) {
	return scanPartner(r.pool.QueryRow(ctx, `
		SELECT `+partnerColumns+` FROM partners p WHERE p.user_id = $1`, userID))
}

// --- журнал выдачи доступа ---

// LogAdminAction пишет строку журнала. Append-only: строку отсюда не правят и
// не удаляют — иначе журнал перестаёт быть журналом.
func (r *PartnerRepo) LogAdminAction(ctx context.Context, a domain.AdminAction) error {
	details, err := json.Marshal(nonNilMap(a.Details))
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO partner_admin_log(partner_id, slug, action, key_prefix, operator, reason, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.PartnerID, a.Slug, a.Action, a.KeyPrefix, a.Operator, a.Reason, details)
	return err
}

// ListAdminLog отдаёт журнал, свежее сверху. Пустой slug — по всем партнёрам.
func (r *PartnerRepo) ListAdminLog(ctx context.Context, slug string, limit int) ([]domain.AdminAction, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, partner_id, slug, action, key_prefix, operator, reason, details, created_at
		FROM partner_admin_log
		WHERE ($1 = '' OR slug = $1)
		ORDER BY created_at DESC
		LIMIT $2`, slug, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.AdminAction{}
	for rows.Next() {
		var a domain.AdminAction
		var details []byte
		if err := rows.Scan(&a.ID, &a.PartnerID, &a.Slug, &a.Action, &a.KeyPrefix,
			&a.Operator, &a.Reason, &details, &a.CreatedAt); err != nil {
			return nil, err
		}
		if len(details) > 0 {
			if err := json.Unmarshal(details, &a.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
