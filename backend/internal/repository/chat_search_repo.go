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
		INSERT INTO chat_searches(chat_id, message_id, raw_query, parsed_query, relaxed, data_freshness, degraded)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		cs.ChatID, cs.MessageID, cs.RawQuery, parsedJSON, cs.Relaxed, cs.DataFreshness, cs.Degraded,
	).Scan(&id)
	return id, err
}

func (r *ChatSearchRepo) UpsertResult(ctx context.Context, res domain.ChatSearchResult) error {
	factsJSON, err := json.Marshal(res.AddressFacts)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO chat_search_results(chat_id, external_id, search_id, price, area, rooms, address_facts, score, explanation)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (chat_id, external_id) DO UPDATE SET
		    search_id = EXCLUDED.search_id,
		    price = EXCLUDED.price,
		    area = EXCLUDED.area,
		    rooms = EXCLUDED.rooms,
		    address_facts = EXCLUDED.address_facts,
		    score = EXCLUDED.score,
		    explanation = EXCLUDED.explanation,
		    updated_at = now()`,
		res.ChatID, res.ExternalID, res.SearchID, res.Price, res.Area, res.Rooms, factsJSON, res.Score, res.Explanation)
	return err
}

func (r *ChatSearchRepo) GetResult(ctx context.Context, chatID uuid.UUID, externalID string) (domain.ChatSearchResult, error) {
	var res domain.ChatSearchResult
	var factsJSON []byte
	err := r.pool.QueryRow(ctx, `
		SELECT chat_id, external_id, search_id, price, area, rooms, address_facts, score, explanation, updated_at
		FROM chat_search_results WHERE chat_id = $1 AND external_id = $2`,
		chatID, externalID,
	).Scan(&res.ChatID, &res.ExternalID, &res.SearchID, &res.Price, &res.Area, &res.Rooms, &factsJSON, &res.Score, &res.Explanation, &res.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ChatSearchResult{}, ErrNotFound
	}
	if err != nil {
		return domain.ChatSearchResult{}, err
	}
	if len(factsJSON) > 0 {
		_ = json.Unmarshal(factsJSON, &res.AddressFacts)
	}
	return res, nil
}
