// evidence_repo.go — READ-ONLY доступ к Python-owned таблице urban_evidence.
// Слои модельные (proxy), а не замеры, поэтому источник едет наружу вместе с
// геометрией: подпись происхождения — часть контракта, а не украшение.
package repository

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type EvidenceRepo struct{ pool *pgxpool.Pool }

func NewEvidenceRepo(pool *pgxpool.Pool) *EvidenceRepo { return &EvidenceRepo{pool: pool} }

// ListByLayers возвращает упрощённую геометрию слоёв внутри bbox.
// Допуск 0.0001 ≈ 10 м на широте Москвы — тот же порядок, что у границ зон.
// Лимит режется ПО СЛОЯМ (row_number по partition), а не общий: иначе плотный
// слой (шум — 46 335 линий) выбрал бы всю квоту и сосед вернулся бы пустым без
// пометки усечения. limit+1 строк берётся намеренно: вызывающий по перебору
// узнаёт об усечении. Упрощение считается уже после среза — не на все 46 тыс.
func (r *EvidenceRepo) ListByLayers(ctx context.Context, city string, layers []string,
	bbox [4]float64, limit int) ([]domain.EvidenceFeature, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT layer, source, weight, db,
		       ST_AsGeoJSON(ST_SimplifyPreserveTopology(geom, 0.0001), 5)
		FROM (
			SELECT layer, source, weight, db, geom,
			       row_number() OVER (PARTITION BY layer ORDER BY source, source_id) AS rn
			FROM urban_evidence
			WHERE city = $1 AND layer = ANY($2)
			  AND geom && ST_MakeEnvelope($3, $4, $5, $6, 4326)
		) t
		WHERE rn <= $7`,
		city, layers, bbox[0], bbox[1], bbox[2], bbox[3], limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.EvidenceFeature
	for rows.Next() {
		var f domain.EvidenceFeature
		if err := rows.Scan(&f.Layer, &f.Source, &f.Weight, &f.DB, &f.GeometryJSON); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
