// display_fields.go — shared gap-filling for data the ML/offline pipeline
// doesn't produce today (name, images, address, formatted floor). Every
// synthesized value here is deliberately honest: never invent a building
// name, a "0% коммуналок" style claim, or any fact not backed by real data.
// See plan §4/§5 for the full rationale and the coordination gaps flagged
// for the ML/data teammate.
package service

import (
	"fmt"
	"math"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
)

const PlaceholderCoverImage = "/static/placeholder-cover.svg"

// SynthName builds an honest display name from structural facts only —
// promote_to_listings never copies a real title, so there is nothing else to use.
func SynthName(rooms *int, area *float64) string {
	switch {
	case rooms != nil && area != nil:
		return fmt.Sprintf("%d-комн, %.0f м²", *rooms, *area)
	case area != nil:
		return fmt.Sprintf("%.0f м²", *area)
	default:
		return "Квартира"
	}
}

func FormatFloor(level, levels *int) string {
	if level != nil && levels != nil {
		return fmt.Sprintf("%d/%d", *level, *levels)
	}
	return ""
}

// RescaleScore defends against ResultItem.score being on two different scales
// depending on whether the reranker degraded (see plan §3/§4): after a real
// rerank it's roughly 0..1; under "reranker" degradation it's a raw RRF score
// (~0.02-0.05), which a naive *100 would render as a broken "3%".
func RescaleScore(score float64, rank int, degraded []string) int {
	for _, d := range degraded {
		if d == "reranker" {
			v := 95 - 5*rank
			if v < 50 {
				v = 50
			}
			return v
		}
	}
	v := int(math.Round(score * 100))
	if v < 1 {
		v = 1
	}
	if v > 99 {
		v = 99
	}
	return v
}

// BuildTags renders only facts actually present in address_facts — never
// fabricates a claim like "0% коммуналок" that isn't backed by real data.
func BuildTags(facts map[string]any) []string {
	var tags []string
	if v, ok := numFact(facts, "walk_min_school"); ok {
		tags = append(tags, fmt.Sprintf("%.0f минут до школы", v))
	}
	if v, ok := numFact(facts, "walk_min_metro"); ok {
		tags = append(tags, fmt.Sprintf("%.0f минут до метро", v))
	}
	if v, ok := numFact(facts, "walk_min_park"); ok {
		tags = append(tags, fmt.Sprintf("%.0f минут до парка", v))
	}
	if v, ok := numFact(facts, "bar_density_500m"); ok {
		tags = append(tags, fmt.Sprintf("%.0f баров в радиусе 500м", v))
	}
	if v, ok := facts["noise_level"].(string); ok && v != "" {
		tags = append(tags, "шум: "+v)
	}
	return tags
}

func numFact(facts map[string]any, key string) (float64, bool) {
	v, ok := facts[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// FinalResultObject mirrors final_result.objects[] from
// frontend/Пайплайн фронт.md §3.
type FinalResultObject struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	CoverImage  string    `json:"cover_image"`
	MatchScore  int       `json:"match_score"`
	Coordinates []float64 `json:"coordinates"`
	PriceFrom   *int64    `json:"price_from"`
	Rooms       *int      `json:"rooms"`
	AreaSqm     *float64  `json:"area_sqm"`
	Floor       string    `json:"floor"`
	Tags        []string  `json:"tags"`
}

// BuildFinalResultObject assembles one result card, skipping (returns false)
// if the listing is missing from the read-only lookup (e.g. deactivated since
// the ML response was computed).
func BuildFinalResultObject(item client.ResultItem, rank int, degraded []string, listings map[string]domain.Listing) (FinalResultObject, bool) {
	l, ok := listings[item.ExternalID]
	if !ok || l.Lon == nil || l.Lat == nil {
		return FinalResultObject{}, false
	}
	return FinalResultObject{
		ID:          item.ExternalID,
		Name:        SynthName(l.Rooms, l.Area),
		CoverImage:  PlaceholderCoverImage,
		MatchScore:  RescaleScore(item.Score, rank, degraded),
		Coordinates: []float64{*l.Lon, *l.Lat},
		PriceFrom:   l.Price,
		Rooms:       l.Rooms,
		AreaSqm:     l.Area,
		Floor:       FormatFloor(l.Level, l.Levels),
		Tags:        BuildTags(item.AddressFacts),
	}, true
}
