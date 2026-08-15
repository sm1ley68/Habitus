package cian

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var ErrMissingOffers = errors.New("Cian response does not contain data.offersSerialized")

// ParseSearchResponse converts Cian's internal response into a small stable
// schema. The source payload is deliberately decoded loosely: Cian has changed
// optional nested types without versioning this internal endpoint.
func ParseSearchResponse(body []byte, collectedAt time.Time) ([]Listing, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode Cian JSON: %w", err)
	}

	offers, ok := sliceAt(root, "data", "offersSerialized")
	if !ok {
		return nil, ErrMissingOffers
	}

	result := make([]Listing, 0, len(offers))
	for _, item := range offers {
		offer, ok := item.(map[string]any)
		if !ok {
			continue
		}
		listing, ok := parseOffer(offer, collectedAt)
		if ok {
			result = append(result, listing)
		}
	}
	return result, nil
}

func parseOffer(offer map[string]any, collectedAt time.Time) (Listing, bool) {
	id := firstString(offer, []string{"cianId"}, []string{"id"})
	description := firstString(offer, []string{"description"})
	if id == "" || description == "" {
		return Listing{}, false
	}

	listing := Listing{
		CianID:             id,
		Description:        description,
		Price:              firstInt64(offer, []string{"bargainTerms", "price"}, []string{"price"}),
		Area:               firstFloat64(offer, []string{"totalArea"}, []string{"area"}),
		Rooms:              firstInt(offer, []string{"roomsCount"}, []string{"rooms"}),
		Floor:              firstInt(offer, []string{"floorNumber"}, []string{"floor"}),
		Floors:             firstInt(offer, []string{"building", "floorsCount"}),
		Address:            parseAddress(offer),
		Metro:              parseMetro(offer),
		ResidentialComplex: firstString(offer, []string{"newbuilding", "name"}),
		BuildingMaterial:   firstString(offer, []string{"building", "materialType"}),
		Deadline:           firstString(offer, []string{"building", "deadline"}),
		Latitude:           firstFloat64(offer, []string{"geo", "coordinates", "lat"}, []string{"coordinates", "lat"}),
		Longitude:          firstFloat64(offer, []string{"geo", "coordinates", "lng"}, []string{"geo", "coordinates", "lon"}, []string{"coordinates", "lng"}, []string{"coordinates", "lon"}),
		URL:                firstString(offer, []string{"fullUrl"}, []string{"url"}),
		CollectedAt:        collectedAt.UTC(),
	}
	return listing, true
}

func parseAddress(offer map[string]any) string {
	if value := firstString(offer, []string{"geo", "userInput"}); value != "" {
		return value
	}
	items, ok := sliceAt(offer, "geo", "address")
	if !ok {
		return ""
	}

	seen := make(map[string]struct{}, len(items))
	parts := make([]string, 0, len(items))
	for _, item := range items {
		part := ""
		if object, ok := item.(map[string]any); ok {
			part = firstString(object, []string{"fullName"}, []string{"name"})
		} else {
			part = scalarString(item)
		}
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func parseMetro(offer map[string]any) []Metro {
	items, ok := sliceAt(offer, "geo", "undergrounds")
	if !ok {
		return []Metro{}
	}
	metro := make([]Metro, 0, len(items))
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			continue
		}
		station := Metro{
			Name:          firstString(object, []string{"name"}),
			Time:          firstInt(object, []string{"time"}),
			TransportType: firstString(object, []string{"transportType"}),
		}
		if station.Name != "" {
			metro = append(metro, station)
		}
	}
	return metro
}

func sliceAt(root map[string]any, path ...string) ([]any, bool) {
	value, ok := valueAt(root, path...)
	if !ok {
		return nil, false
	}
	items, ok := value.([]any)
	return items, ok
}

func valueAt(root map[string]any, path ...string) (any, bool) {
	var value any = root
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		value, ok = object[key]
		if !ok || value == nil {
			return nil, false
		}
	}
	return value, true
}

func firstString(root map[string]any, paths ...[]string) string {
	for _, path := range paths {
		if value, ok := valueAt(root, path...); ok {
			if text := scalarString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func scalarString(value any) string {
	switch value := value.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case map[string]any:
		for _, key := range []string{"name", "value", "title"} {
			if text := scalarString(value[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstInt(root map[string]any, paths ...[]string) *int {
	for _, path := range paths {
		if value, ok := valueAt(root, path...); ok {
			if number, ok := toFloat64(value); ok && number >= math.MinInt && number <= math.MaxInt {
				result := int(number)
				return &result
			}
		}
	}
	return nil
}

func firstInt64(root map[string]any, paths ...[]string) *int64 {
	for _, path := range paths {
		if value, ok := valueAt(root, path...); ok {
			if number, ok := toFloat64(value); ok && number >= math.MinInt64 && number <= math.MaxInt64 {
				result := int64(number)
				return &result
			}
		}
	}
	return nil
}

func firstFloat64(root map[string]any, paths ...[]string) *float64 {
	for _, path := range paths {
		if value, ok := valueAt(root, path...); ok {
			if result, ok := toFloat64(value); ok {
				return &result
			}
		}
	}
	return nil
}

func toFloat64(value any) (float64, bool) {
	var (
		result float64
		err    error
	)
	switch value := value.(type) {
	case json.Number:
		result, err = value.Float64()
	case string:
		result, err = strconv.ParseFloat(strings.TrimSpace(value), 64)
	case float64:
		result = value
	default:
		return 0, false
	}
	return result, err == nil && !math.IsNaN(result) && !math.IsInf(result, 0)
}
