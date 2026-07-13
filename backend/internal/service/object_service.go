// object_service.go — GET /objects/{id}?chat_id=. See plan §5: the passport
// is assembled from what a prior search stream already persisted for this
// (chat_id, external_id) pair, never a second ML call.
package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/repository"
)

type Block struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Icon        string `json:"icon,omitempty"`
	Score       string `json:"score"`
	Description string `json:"description"`
}

type LifestyleAnalysis struct {
	MatchScore int     `json:"match_score"`
	Summary    string  `json:"summary"`
	Blocks     []Block `json:"blocks"`
}

type ObjectPassport struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Address           string            `json:"address"`
	Price             *int64            `json:"price"`
	Rooms             *int              `json:"rooms"`
	AreaSqm           *float64          `json:"area_sqm"`
	Floor             string            `json:"floor"`
	Images            []string          `json:"images"`
	Coordinates       []float64         `json:"coordinates"`
	LifestyleAnalysis LifestyleAnalysis `json:"lifestyle_analysis"`
}

type ObjectService struct {
	chats    *ChatService
	results  *repository.ChatSearchRepo
	listings *repository.ListingRepo
}

func NewObjectService(chats *ChatService, results *repository.ChatSearchRepo, listings *repository.ListingRepo) *ObjectService {
	return &ObjectService{chats: chats, results: results, listings: listings}
}

func (s *ObjectService) GetPassport(ctx context.Context, userID, chatID uuid.UUID, objectID string) (ObjectPassport, error) {
	if _, err := s.chats.GetOwned(ctx, userID, chatID); err != nil {
		return ObjectPassport{}, err
	}

	res, err := s.results.GetResult(ctx, chatID, objectID)
	if errors.Is(err, repository.ErrNotFound) {
		return ObjectPassport{}, apperr.ObjectNotFound()
	}
	if err != nil {
		return ObjectPassport{}, err
	}

	listing, err := s.listings.GetByExternalID(ctx, objectID)
	if err != nil {
		// Defensive only: chat_search_results.external_id only ever comes from
		// a real listings row written moments earlier during a search stream.
		return ObjectPassport{}, apperr.ObjectNotFound()
	}

	var coords []float64
	if listing.Lon != nil && listing.Lat != nil {
		coords = []float64{*listing.Lon, *listing.Lat}
	}

	return ObjectPassport{
		ID:          objectID,
		Name:        SynthName(listing.Rooms, listing.Area),
		Address:     "", // no address text anywhere in the pipeline yet — honest placeholder, see plan §5
		Price:       listing.Price,
		Rooms:       listing.Rooms,
		AreaSqm:     listing.Area,
		Floor:       FormatFloor(listing.Level, listing.Levels),
		Images:      []string{PlaceholderCoverImage},
		Coordinates: coords,
		LifestyleAnalysis: LifestyleAnalysis{
			MatchScore: RescaleScoreFromStored(res.Score),
			Summary:    res.Explanation,
			Blocks:     buildBlocks(res.AddressFacts),
		},
	}, nil
}

// RescaleScoreFromStored applies the same defensive rescale as RescaleScore,
// but the stored score has no rank/degraded context anymore by the time the
// passport is read back — treat it as already-normalized (0..1) since that's
// the common case, and clamp defensively either way.
func RescaleScoreFromStored(score float64) int {
	return RescaleScore(score, 0, nil)
}

func buildBlocks(facts map[string]any) []Block {
	blocks := []Block{}

	if hasAny(facts, "walk_min_school", "walk_min_metro") {
		blocks = append(blocks, Block{
			Key: "logistics", Title: "Логистика и школы", Icon: "school",
			Score:       walkScore(facts),
			Description: logisticsDescription(facts),
		})
	}
	if hasAny(facts, "bar_density_500m") {
		blocks = append(blocks, Block{
			Key: "social_environment", Title: "Окружение", Icon: "users",
			Score:       barScore(facts),
			Description: socialDescription(facts),
		})
	}
	if hasAny(facts, "window_orientation", "noise_level") {
		blocks = append(blocks, Block{
			Key: "view_and_climate", Title: "Вид и климат", Icon: "sun",
			Score:       noiseScore(facts),
			Description: climateDescription(facts),
		})
	}
	return blocks
}

func hasAny(facts map[string]any, keys ...string) bool {
	for _, k := range keys {
		if v, ok := facts[k]; ok && v != nil {
			return true
		}
	}
	return false
}

func walkScore(facts map[string]any) string {
	v, ok := numFact(facts, "walk_min_school")
	if !ok {
		v, ok = numFact(facts, "walk_min_metro")
	}
	if !ok {
		return "B"
	}
	switch {
	case v <= 10:
		return "A"
	case v <= 15:
		return "B+"
	case v <= 20:
		return "B"
	default:
		return "C"
	}
}

func logisticsDescription(facts map[string]any) string {
	if v, ok := numFact(facts, "walk_min_school"); ok {
		return formatMinutes(v) + " до школы пешком."
	}
	if v, ok := numFact(facts, "walk_min_metro"); ok {
		return formatMinutes(v) + " до метро пешком."
	}
	return ""
}

// barScore reuses the exact ">2 bars within 200m" threshold that
// habitus/geo/enrich.py already uses for noise_level, rather than inventing a
// new cutoff — see plan §5.
func barScore(facts map[string]any) string {
	v, ok := numFact(facts, "bar_density_500m")
	if !ok {
		return "B"
	}
	switch {
	case v == 0:
		return "A"
	case v <= 2:
		return "B"
	default:
		return "C"
	}
}

func socialDescription(facts map[string]any) string {
	if v, ok := numFact(facts, "bar_density_500m"); ok {
		return formatCount(v) + " баров/алкомаркетов в радиусе 500 м."
	}
	return ""
}

func noiseScore(facts map[string]any) string {
	if lvl, ok := facts["noise_level"].(string); ok {
		switch lvl {
		case "low":
			return "A-"
		case "medium":
			return "B"
		case "high":
			return "C"
		}
	}
	return "B"
}

func climateDescription(facts map[string]any) string {
	var parts []string
	if ors, ok := facts["window_orientation"]; ok {
		if list, ok := ors.([]any); ok && len(list) > 0 {
			parts = append(parts, "Окна ориентированы на разные стороны света.")
			_ = list
		}
	}
	if lvl, ok := facts["noise_level"].(string); ok && lvl != "" {
		parts = append(parts, "Уровень шума: "+lvl+".")
	}
	if len(parts) == 0 {
		return ""
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

func formatMinutes(v float64) string {
	return trimFloat(v) + " минут"
}

func formatCount(v float64) string {
	return trimFloat(v)
}

func trimFloat(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}
