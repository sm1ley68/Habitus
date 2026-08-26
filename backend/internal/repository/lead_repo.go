package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

// ErrDuplicateLead — этот покупатель уже отправлял заявку по этому объявлению.
var ErrDuplicateLead = errors.New("lead already sent")

type LeadRepo struct {
	pool *pgxpool.Pool
}

func NewLeadRepo(pool *pgxpool.Pool) *LeadRepo {
	return &LeadRepo{pool: pool}
}

func (r *LeadRepo) Create(ctx context.Context, l domain.Lead) (domain.Lead, error) {
	var out domain.Lead
	err := r.pool.QueryRow(ctx, `
		INSERT INTO leads(listing_id, seller_id, buyer_id, external_id, name, contact, message)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, listing_id, seller_id, buyer_id, external_id, name, contact, message, created_at`,
		l.ListingID, l.SellerID, l.BuyerID, l.ExternalID, l.Name, l.Contact, l.Message,
	).Scan(&out.ID, &out.ListingID, &out.SellerID, &out.BuyerID, &out.ExternalID,
		&out.Name, &out.Contact, &out.Message, &out.CreatedAt)
	if err != nil {
		// Повтор ловим на уникальном индексе, а не проверкой-перед-вставкой:
		// две одновременные отправки иначе обе прошли бы.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.Lead{}, ErrDuplicateLead
		}
		return domain.Lead{}, err
	}
	return out, nil
}

// ListForSeller отдаёт заявки продавца, свежие сверху, вместе с адресом
// объявления. total считается тем же запросом через оконную функцию —
// отдельный COUNT(*) удваивал бы поход в БД ради одного числа.
func (r *LeadRepo) ListForSeller(ctx context.Context, sellerID uuid.UUID,
	limit, offset int) ([]domain.Lead, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT l.id, l.listing_id, l.seller_id, l.buyer_id, l.external_id,
		       ol.address, l.name, l.contact, l.message, l.created_at,
		       COUNT(*) OVER () AS total
		FROM leads l
		JOIN owner_listings ol ON ol.id = l.listing_id
		WHERE l.seller_id = $1
		ORDER BY l.created_at DESC
		LIMIT $2 OFFSET $3`, sellerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]domain.Lead, 0, limit)
	total := 0
	for rows.Next() {
		var l domain.Lead
		if err := rows.Scan(&l.ID, &l.ListingID, &l.SellerID, &l.BuyerID, &l.ExternalID,
			&l.Address, &l.Name, &l.Contact, &l.Message, &l.CreatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}
