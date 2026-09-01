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
//
// Выборка скоупится по городу и (если вьюпорт передан) по bbox — тем же
// правилом, что listings и urban_evidence. Пока в `poi` лежала одна Москва,
// запрос по одному kind был безобиден; с наполнением Петербурга он начал
// возить чужой город на карту — 75 станций, 1127 школ и так далее лишними
// фичами в каждый вьюпорт. Индекс под это уже есть: poi_city_kind_ix.
//
// bbox = nil означает «весь город», а не «пусто»: у POI, в отличие от
// urban_evidence, объём слоя на город измеряется тысячами точек, а не
// десятками тысяч линий, и первый кадр карты без вьюпорта показывать честнее,
// чем пустоту.
func (r *POIRepo) ListByKinds(ctx context.Context, kinds []string, city string,
	bbox *[4]float64) ([]domain.POI, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	sql := `
		SELECT kind, COALESCE(name, ''), ST_X(geom), ST_Y(geom)
		FROM poi WHERE kind = ANY($1) AND city = $2 AND geom IS NOT NULL`
	args := []any{kinds, city}
	if bbox != nil {
		sql += ` AND geom && ST_MakeEnvelope($3, $4, $5, $6, 4326)`
		args = append(args, bbox[0], bbox[1], bbox[2], bbox[3])
	}
	rows, err := r.pool.Query(ctx, sql, args...)
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
