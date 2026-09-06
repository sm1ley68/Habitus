// partner_search_repo.go — поиск партнёра и его результаты. Форма повторяет
// ChatSearchRepo: тот же снимок ответа ML и тот же ленивый кэш досье, только
// вместо чата — самостоятельный ресурс «поиск».
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

type PartnerSearchRepo struct {
	pool *pgxpool.Pool
}

func NewPartnerSearchRepo(pool *pgxpool.Pool) *PartnerSearchRepo {
	return &PartnerSearchRepo{pool: pool}
}

func (r *PartnerSearchRepo) Insert(ctx context.Context, s domain.PartnerSearch) (uuid.UUID, error) {
	parsedJSON, err := json.Marshal(nonNilMap(s.ParsedQuery))
	if err != nil {
		return uuid.Nil, err
	}
	// area_geojson остаётся NULL, когда зоны нет: пустой объект читался бы как
	// «зона посчитана и пуста», а это разные вещи.
	var areaJSON []byte
	if s.AreaGeoJSON != nil {
		if areaJSON, err = json.Marshal(s.AreaGeoJSON); err != nil {
			return uuid.Nil, err
		}
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO partner_searches(partner_id, query, city, parsed_query, relaxed,
		                             degraded, notes, data_freshness, area_label,
		                             area_geojson, explanation, total)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id`,
		s.PartnerID, s.Query, s.City, parsedJSON, nonNilStrings(s.Relaxed),
		nonNilStrings(s.Degraded), nonNilStrings(s.Notes), s.DataFreshness,
		s.AreaLabel, nullableJSON(areaJSON), s.Explanation, s.Total,
	).Scan(&id)
	return id, err
}

// GetOwned читает поиск, принадлежащий именно этому партнёру: id поиска в URL
// — угадываемый параметр, и проверка владения здесь, а не в хендлере.
func (r *PartnerSearchRepo) GetOwned(ctx context.Context, partnerID, id uuid.UUID) (domain.PartnerSearch, error) {
	var s domain.PartnerSearch
	var parsedJSON, areaJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, partner_id, query, city, parsed_query, relaxed, degraded, notes,
		       data_freshness, area_label, area_geojson, explanation, total, created_at
		FROM partner_searches WHERE id = $1 AND partner_id = $2`, id, partnerID,
	).Scan(&s.ID, &s.PartnerID, &s.Query, &s.City, &parsedJSON, &s.Relaxed, &s.Degraded,
		&s.Notes, &s.DataFreshness, &s.AreaLabel, &areaJSON, &s.Explanation, &s.Total, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PartnerSearch{}, ErrNotFound
	}
	if err != nil {
		return domain.PartnerSearch{}, err
	}
	if len(parsedJSON) > 0 {
		if err := json.Unmarshal(parsedJSON, &s.ParsedQuery); err != nil {
			return domain.PartnerSearch{}, err
		}
	}
	if len(areaJSON) > 0 {
		if err := json.Unmarshal(areaJSON, &s.AreaGeoJSON); err != nil {
			return domain.PartnerSearch{}, err
		}
	}
	return s, nil
}

func (r *PartnerSearchRepo) InsertResults(ctx context.Context, searchID uuid.UUID,
	rows []domain.PartnerSearchResult) error {
	batch := &pgx.Batch{}
	for _, res := range rows {
		factsJSON, err := json.Marshal(nonNilMap(res.AddressFacts))
		if err != nil {
			return err
		}
		batch.Queue(`
			INSERT INTO partner_search_results(search_id, external_id, rank, price, area,
			                                   rooms, address_facts, score, match_score)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (search_id, external_id) DO NOTHING`,
			searchID, res.ExternalID, res.Rank, res.Price, res.Area, res.Rooms,
			factsJSON, res.Score, res.MatchScore)
	}
	br := r.pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// ListResults — страница результатов поиска, порядок ранга сохранён.
// total считается отдельным COUNT(*), а не оконной функцией: на странице за
// концом списка окно не вернёт ни строки и total ложно схлопнется в 0.
func (r *PartnerSearchRepo) ListResults(ctx context.Context, searchID uuid.UUID,
	limit, offset int) ([]domain.PartnerSearchResult, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM partner_search_results WHERE search_id = $1`, searchID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT search_id, external_id, rank, price, area, rooms, address_facts,
		       score, match_score, dossier, dossier_version, dossier_updated_at
		FROM partner_search_results
		WHERE search_id = $1
		ORDER BY rank
		LIMIT $2 OFFSET $3`, searchID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []domain.PartnerSearchResult{}
	for rows.Next() {
		res, err := scanPartnerResult(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, res)
	}
	return out, total, rows.Err()
}

func (r *PartnerSearchRepo) GetResult(ctx context.Context, searchID uuid.UUID,
	externalID string) (domain.PartnerSearchResult, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT search_id, external_id, rank, price, area, rooms, address_facts,
		       score, match_score, dossier, dossier_version, dossier_updated_at
		FROM partner_search_results WHERE search_id = $1 AND external_id = $2`,
		searchID, externalID)
	res, err := scanPartnerResult(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.PartnerSearchResult{}, ErrNotFound
	}
	return res, err
}

func (r *PartnerSearchRepo) SaveDossier(ctx context.Context, searchID uuid.UUID,
	externalID, version string, dossier map[string]any) error {
	dossierJSON, err := json.Marshal(dossier)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		UPDATE partner_search_results
		SET dossier = $3, dossier_version = $4, dossier_updated_at = now()
		WHERE search_id = $1 AND external_id = $2`,
		searchID, externalID, dossierJSON, version)
	return err
}

// SweepSearches убирает поиски старше ttl вместе с их результатами (каскад по
// FK). Партнёрский поиск — рабочий контекст интеграции, а не архив: держать
// его вечно значит вечно хранить и сырую выдачу под него.
func (r *PartnerSearchRepo) SweepSearches(ctx context.Context, ttl time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM partner_searches WHERE created_at < now() - $1::interval`, ttl.String())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func scanPartnerResult(row pgx.Row) (domain.PartnerSearchResult, error) {
	var res domain.PartnerSearchResult
	var factsJSON, dossierJSON []byte
	var version *string
	err := row.Scan(&res.SearchID, &res.ExternalID, &res.Rank, &res.Price, &res.Area,
		&res.Rooms, &factsJSON, &res.Score, &res.MatchScore, &dossierJSON, &version,
		&res.DossierUpdatedAt)
	if err != nil {
		return domain.PartnerSearchResult{}, err
	}
	if len(factsJSON) > 0 {
		if err := json.Unmarshal(factsJSON, &res.AddressFacts); err != nil {
			return domain.PartnerSearchResult{}, err
		}
	}
	if len(dossierJSON) > 0 {
		if err := json.Unmarshal(dossierJSON, &res.Dossier); err != nil {
			return domain.PartnerSearchResult{}, err
		}
	}
	if version != nil {
		res.DossierVersion = *version
	}
	return res, nil
}

// nonNilStrings — nil-срез в text[] уезжает как NULL, а колонки объявлены
// NOT NULL DEFAULT '{}'. Пустой массив и есть честное «пусто».
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// nullableJSON отдаёт nil вместо пустого []byte: иначе pgx пишет ” и падает
// на jsonb.
func nullableJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
