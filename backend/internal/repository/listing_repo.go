// listing_repo.go — READ-ONLY access to the Python-owned `listings` table
// (habitus/db/schema.sql). Never write here; the ML/offline pipeline is the
// sole owner of this table's contents and schema.
package repository

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type ListingRepo struct {
	pool *pgxpool.Pool
}

func NewListingRepo(pool *pgxpool.Pool) *ListingRepo {
	return &ListingRepo{pool: pool}
}

func scanListing(rows pgx.Rows) (domain.Listing, error) {
	var l domain.Listing
	err := rows.Scan(&l.ExternalID, &l.Price, &l.Area, &l.Rooms, &l.Level, &l.Levels, &l.Lon, &l.Lat)
	return l, err
}

// GetByExternalIDs batch-fetches display fields for a set of listings, keyed by
// external_id. Missing IDs (e.g. deactivated since the ML response was built)
// are simply absent from the returned map — callers must skip them, not error.
func (r *ListingRepo) GetByExternalIDs(ctx context.Context, ids []string) (map[string]domain.Listing, error) {
	out := make(map[string]domain.Listing, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT external_id, price, area, rooms, level, levels, ST_X(geom), ST_Y(geom)
		FROM listings WHERE external_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		out[l.ExternalID] = l
	}
	return out, rows.Err()
}

func (r *ListingRepo) GetByExternalID(ctx context.Context, id string) (domain.Listing, error) {
	m, err := r.GetByExternalIDs(ctx, []string{id})
	if err != nil {
		return domain.Listing{}, err
	}
	l, ok := m[id]
	if !ok {
		return domain.Listing{}, errors.New("listing not found")
	}
	return l, nil
}
