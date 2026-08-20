package repository

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

type ChatSearchRepo struct {
	pool *pgxpool.Pool
}

func NewChatSearchRepo(pool *pgxpool.Pool) *ChatSearchRepo {
	return &ChatSearchRepo{pool: pool}
}

func (r *ChatSearchRepo) InsertSearch(ctx context.Context, cs domain.ChatSearch) (uuid.UUID, error) {
	parsedJSON, err := json.Marshal(cs.ParsedQuery)
	if err != nil {
		return uuid.Nil, err
	}
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, `
		INSERT INTO chat_searches(chat_id, message_id, raw_query, parsed_query, relaxed, data_freshness, degraded, intent)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`,
		cs.ChatID, cs.MessageID, cs.RawQuery, parsedJSON, cs.Relaxed, cs.DataFreshness, cs.Degraded, cs.Intent,
	).Scan(&id)
	return id, err
}

// LastParsedQuery отдаёт parsed_query последнего поиска чата — контекст
// prev_parsed для multi-turn запроса к ML (Task 4). Поисков в чате ещё не
// было — это обычное состояние нового чата, а не ошибка: возвращаем (nil, nil).
func (r *ChatSearchRepo) LastParsedQuery(ctx context.Context, chatID uuid.UUID) (map[string]any, error) {
	var parsedJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT parsed_query FROM chat_searches
		WHERE chat_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, chatID,
	).Scan(&parsedJSON)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(parsedJSON) == 0 {
		return nil, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal(parsedJSON, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (r *ChatSearchRepo) UpsertResult(ctx context.Context, res domain.ChatSearchResult) error {
	factsJSON, err := json.Marshal(res.AddressFacts)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO chat_search_results(chat_id, external_id, search_id, price, area,
		                                rooms, address_facts, score, match_score, explanation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (chat_id, external_id) DO UPDATE SET
		    search_id = EXCLUDED.search_id,
		    price = EXCLUDED.price,
		    area = EXCLUDED.area,
		    rooms = EXCLUDED.rooms,
		    address_facts = EXCLUDED.address_facts,
		    score = EXCLUDED.score,
		    match_score = EXCLUDED.match_score,
		    explanation = EXCLUDED.explanation,
		    dossier = NULL,
		    dossier_version = NULL,
		    dossier_updated_at = NULL,
		    updated_at = now()`,
		res.ChatID, res.ExternalID, res.SearchID, res.Price, res.Area, res.Rooms, factsJSON, res.Score, res.MatchScore, res.Explanation)
	return err
}

func (r *ChatSearchRepo) GetResult(ctx context.Context, chatID uuid.UUID, externalID string) (domain.ChatSearchResult, error) {
	var res domain.ChatSearchResult
	var factsJSON, dossierJSON []byte
	var dossierVersion *string
	err := r.pool.QueryRow(ctx, `
		SELECT chat_id, external_id, search_id, price, area, rooms, address_facts,
		       score, match_score, explanation, dossier, dossier_version,
		       dossier_updated_at, updated_at
		FROM chat_search_results WHERE chat_id = $1 AND external_id = $2`,
		chatID, externalID,
	).Scan(&res.ChatID, &res.ExternalID, &res.SearchID, &res.Price, &res.Area,
		&res.Rooms, &factsJSON, &res.Score, &res.MatchScore, &res.Explanation, &dossierJSON,
		&dossierVersion, &res.DossierUpdatedAt, &res.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChatSearchResult{}, ErrNotFound
	}
	if err != nil {
		return domain.ChatSearchResult{}, err
	}
	if len(factsJSON) > 0 {
		_ = json.Unmarshal(factsJSON, &res.AddressFacts)
	}
	if len(dossierJSON) > 0 {
		_ = json.Unmarshal(dossierJSON, &res.Dossier)
	}
	if dossierVersion != nil {
		res.DossierVersion = *dossierVersion
	}
	return res, nil
}

// ListResults отдаёт сохранённые объекты ПОСЛЕДНЕГО поиска чата постранично,
// отсортированные по score DESC, external_id — «показать ещё» (Task 7): весь
// набор из ответа ML уже лежит в chat_search_results, второй поход в ML не
// нужен. Поисков в чате ещё не было — пустой список и total=0, не ошибка.
func (r *ChatSearchRepo) ListResults(ctx context.Context, chatID uuid.UUID, limit, offset int) ([]domain.ChatSearchResult, int, error) {
	var searchID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT id FROM chat_searches
		WHERE chat_id = $1
		ORDER BY created_at DESC
		LIMIT 1`, chatID,
	).Scan(&searchID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	var total int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM chat_search_results
		WHERE chat_id = $1 AND search_id = $2`, chatID, searchID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT chat_id, external_id, search_id, price, area, rooms, address_facts,
		       score, match_score, explanation, updated_at
		FROM chat_search_results
		WHERE chat_id = $1 AND search_id = $2
		ORDER BY score DESC, external_id
		LIMIT $3 OFFSET $4`, chatID, searchID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []domain.ChatSearchResult
	for rows.Next() {
		var res domain.ChatSearchResult
		var factsJSON []byte
		if err := rows.Scan(&res.ChatID, &res.ExternalID, &res.SearchID, &res.Price,
			&res.Area, &res.Rooms, &factsJSON, &res.Score, &res.MatchScore,
			&res.Explanation, &res.UpdatedAt); err != nil {
			return nil, 0, err
		}
		if len(factsJSON) > 0 {
			_ = json.Unmarshal(factsJSON, &res.AddressFacts)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

func (r *ChatSearchRepo) GetSearch(ctx context.Context, id uuid.UUID) (domain.ChatSearch, error) {
	var cs domain.ChatSearch
	var parsedJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT id, chat_id, message_id, raw_query, parsed_query, relaxed,
		       data_freshness, degraded, intent, created_at
		FROM chat_searches WHERE id=$1`, id,
	).Scan(&cs.ID, &cs.ChatID, &cs.MessageID, &cs.RawQuery, &parsedJSON,
		&cs.Relaxed, &cs.DataFreshness, &cs.Degraded, &cs.Intent, &cs.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChatSearch{}, ErrNotFound
	}
	if err != nil {
		return domain.ChatSearch{}, err
	}
	if len(parsedJSON) > 0 {
		_ = json.Unmarshal(parsedJSON, &cs.ParsedQuery)
	}
	return cs, nil
}

func (r *ChatSearchRepo) SaveDossier(ctx context.Context, chatID, searchID uuid.UUID,
	externalID, version string, dossier map[string]any) error {
	b, err := json.Marshal(dossier)
	if err != nil {
		return err
	}
	result, err := r.pool.Exec(ctx, `
		UPDATE chat_search_results
		SET dossier=$4, dossier_version=$5, dossier_updated_at=now()
		WHERE chat_id=$1 AND external_id=$2 AND search_id=$3`,
		chatID, externalID, searchID, b, version)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
