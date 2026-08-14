// Package geojson holds the minimal RFC 7946 shapes the API needs to emit —
// coordinates are always [lng, lat] (WGS84), per frontend/Пайплайн фронт.md.
package geojson

import "encoding/json"

type Geometry struct {
	Type        string `json:"type"`
	Coordinates any    `json:"coordinates"`
}

type Feature struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties"`
	Geometry   Geometry       `json:"geometry"`
}

type FeatureCollection struct {
	Type     string    `json:"type"`
	Features []Feature `json:"features"`
}

func NewFeatureCollection() FeatureCollection {
	return FeatureCollection{Type: "FeatureCollection", Features: []Feature{}}
}

func Point(lon, lat float64, props map[string]any) Feature {
	if props == nil {
		props = map[string]any{}
	}
	return Feature{
		Type:       "Feature",
		Properties: props,
		Geometry:   Geometry{Type: "Point", Coordinates: []float64{lon, lat}},
	}
}

func Polygon(ring [][2]float64, props map[string]any) Feature {
	if props == nil {
		props = map[string]any{}
	}
	coords := make([][]float64, len(ring))
	for i, p := range ring {
		coords[i] = []float64{p[0], p[1]}
	}
	return Feature{
		Type:       "Feature",
		Properties: props,
		Geometry:   Geometry{Type: "Polygon", Coordinates: [][][]float64{coords}},
	}
}

// RawFeature оборачивает уже сериализованный GeoJSON из ST_AsGeoJSON —
// разбирать и пересобирать его на стороне Go незачем. Битая геометрия даёт
// фичу без geometry, а не панику: слой деградирует, ответ остаётся валидным.
func RawFeature(geometryJSON string, props map[string]any) Feature {
	if props == nil {
		props = map[string]any{}
	}
	var g Geometry
	if err := json.Unmarshal([]byte(geometryJSON), &g); err != nil {
		return Feature{Type: "Feature", Properties: props}
	}
	return Feature{Type: "Feature", Properties: props, Geometry: g}
}
