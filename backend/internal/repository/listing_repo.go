// listing_repo.go — READ-ONLY access to the Python-owned `listings` table
// (habitus/db/schema.sql). Never write here; the ML/offline pipeline is the
// sole owner of this table's contents and schema.
package repository

import (
	"context"
	"errors"
	"time"

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
	err := rows.Scan(&l.ExternalID, &l.Price, &l.Area, &l.Rooms, &l.Level, &l.Levels,
		&l.Lon, &l.Lat, &l.Address, &l.MetroStation, &l.Photos,
		&l.WalkMinSchool, &l.WalkMinMetro, &l.WalkMinPark, &l.BarDensity500m, &l.NoiseLevel)
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
		SELECT external_id, price, area, rooms, level, levels,
		       ST_X(geom), ST_Y(geom), address, metro_station, photos,
		       walk_min_school, walk_min_metro, walk_min_park,
		       bar_density_500m, noise_level
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

// GetUpdatedAt читает listings.updated_at одного объекта — нужен только чтобы
// понять, протухло ли закэшированное досье (object_service.go, Task 7),
// поэтому не тянет остальные колонки, как GetByExternalIDs. Объекта в
// listings нет — (nil, nil): сравнивать не с чем, свежесть решает только TTL.
func (r *ListingRepo) GetUpdatedAt(ctx context.Context, externalID string) (*time.Time, error) {
	var updatedAt time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT updated_at FROM listings WHERE external_id = $1`, externalID,
	).Scan(&updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &updatedAt, nil
}

// ListInBBox — объявления с координатами внутри вьюпорта, чтобы карта могла
// показать ЛЮБОЙ объект города, а не только попавший в выдачу подбора.
// Порядок стабильный (по external_id): при упоре в лимит один и тот же вьюпорт
// обязан отдавать один и тот же набор, иначе точки мигают при каждом панораме.
func (r *ListingRepo) ListInBBox(ctx context.Context, city string, bbox [4]float64,
	limit int) ([]domain.Listing, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT external_id, price, area, rooms, level, levels,
		       ST_X(geom), ST_Y(geom), address, metro_station, photos,
		       walk_min_school, walk_min_metro, walk_min_park,
		       bar_density_500m, noise_level
		FROM listings
		WHERE is_active AND city = $1 AND geom IS NOT NULL
		  AND geom && ST_MakeEnvelope($2, $3, $4, $5, 4326)
		ORDER BY external_id
		LIMIT $6`,
		city, bbox[0], bbox[1], bbox[2], bbox[3], limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Listing
	for rows.Next() {
		l, err := scanListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *ListingRepo) SnapshotByExternalID(ctx context.Context, externalID string) (domain.ListingSnapshot, error) {
	var s domain.ListingSnapshot
	err := r.pool.QueryRow(ctx, `
		SELECT external_id, source, coalesce(city, 'msk'), price, area, kitchen_area,
		       rooms, level, levels, coalesce(address, ''), coalesce(description, ''),
		       ST_X(geom), ST_Y(geom), coalesce(photos, '{}'),
		       coalesce(window_orientation, '{}'), coalesce(source_url, ''), owner_managed
		FROM listings WHERE external_id = $1`, externalID).Scan(
		&s.ExternalID, &s.Source, &s.City, &s.Price, &s.Area, &s.KitchenArea,
		&s.Rooms, &s.Level, &s.Levels, &s.Address, &s.Description,
		&s.Lng, &s.Lat, &s.Photos, &s.WindowOrientation, &s.SourceURL, &s.OwnerManaged)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ListingSnapshot{}, ErrNotFound
	}
	return s, err
}

// FindSimilar ищет ту же квартиру, перевыставленную под другим id: тот же дом
// (150 м), те же комнаты и этаж, площадь в пределах метра. Без координат, комнат
// или этажа сравнивать не с чем — возвращаем пусто, чтобы не сыпать ложными дублями.
func (r *ListingRepo) FindSimilar(ctx context.Context, lng, lat *float64,
	rooms, level *int, area *float32, excludeExternalID string) ([]domain.SimilarListing, error) {
	if lng == nil || lat == nil || rooms == nil || level == nil || area == nil {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT external_id, coalesce(address, ''), price, area
		FROM listings
		WHERE is_active
		  AND rooms = $3
		  AND level = $4
		  AND abs(area - $5) <= 1.0
		  AND external_id <> $6
		  AND ST_DWithin(geom::geography, ST_SetSRID(ST_MakePoint($1, $2), 4326)::geography, 150)
		ORDER BY geom <-> ST_SetSRID(ST_MakePoint($1, $2), 4326)
		LIMIT 3`, *lng, *lat, *rooms, *level, *area, excludeExternalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.SimilarListing{}
	for rows.Next() {
		var s domain.SimilarListing
		if err := rows.Scan(&s.ExternalID, &s.Address, &s.Price, &s.Area); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
