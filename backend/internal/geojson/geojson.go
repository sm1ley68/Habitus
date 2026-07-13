// Package geojson holds the minimal RFC 7946 shapes the API needs to emit —
// coordinates are always [lng, lat] (WGS84), per frontend/Пайплайн фронт.md.
package geojson

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
