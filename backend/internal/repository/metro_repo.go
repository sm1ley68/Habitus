// metro_repo.go — READ-ONLY доступ к Python-owned таблицам графа метро
// (metro_line, metro_line_geom — созданы Задачей 5, наполняются Задачей 6).
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type MetroRepo struct {
	pool *pgxpool.Pool
}

func NewMetroRepo(pool *pgxpool.Pool) *MetroRepo {
	return &MetroRepo{pool: pool}
}

// ListLines returns rail lines with their drawing geometry for one city.
// metro_line_geom.geom is nullable — a line may legitimately have no
// geometry. Such lines are skipped here rather than surfaced with an empty
// or zero-coordinate LineString: absence must stay absence, not a synthetic
// feature that draws nothing (or draws garbage) on the map. colour is left
// NULL as-is (no COALESCE to ""): an invented empty string would be its own
// small synthetic value standing in for "no colour", asymmetric with how
// Задача 14 keeps MetroSegment.colour nullable on the same underlying data.
func (r *MetroRepo) ListLines(ctx context.Context, city string) ([]domain.MetroLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT ml.ref, ml.name, ml.system, ml.colour,
		       ST_AsGeoJSON(g.geom)
		FROM metro_line ml
		JOIN metro_line_geom g ON g.line_id = ml.id
		WHERE ml.city = $1 AND g.geom IS NOT NULL`, city)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.MetroLine
	for rows.Next() {
		var l domain.MetroLine
		if err := rows.Scan(&l.Ref, &l.Name, &l.System, &l.Colour, &l.GeometryJSON); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
