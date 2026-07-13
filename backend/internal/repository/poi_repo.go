// poi_repo.go — READ-ONLY access to the Python-owned `poi` table.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type POIRepo struct {
	pool *pgxpool.Pool
}

func NewPOIRepo(pool *pgxpool.Pool) *POIRepo {
	return &POIRepo{pool: pool}
}

// ListByKinds returns POI points for the given `poi.kind` values. Real kind
// values written by the offline pipeline (habitus/geo/osm_extract.py) are
// exactly: school, bar, alcohol, park, metro — mapping from the frontend's
// geo-layer enum to these lives in the geo-layers service, not here.
func (r *POIRepo) ListByKinds(ctx context.Context, kinds []string) ([]domain.POI, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT kind, COALESCE(name, ''), ST_X(geom), ST_Y(geom)
		FROM poi WHERE kind = ANY($1) AND geom IS NOT NULL`, kinds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.POI
	for rows.Next() {
		var p domain.POI
		if err := rows.Scan(&p.Kind, &p.Name, &p.Lon, &p.Lat); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
