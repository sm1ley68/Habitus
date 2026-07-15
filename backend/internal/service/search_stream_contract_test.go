package service

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSuggestedAreasFallsBackToCustomPoint(t *testing.T) {
	want := [2]float64{37.6, 55.7}
	got := BuildSuggestedAreas(nil, &want)
	if len(got.Features) != 1 || got.Features[0].Geometry.Type != "Polygon" {
		t.Fatalf("BuildSuggestedAreas() = %#v; want one polygon", got)
	}
	ring := got.Features[0].Geometry.Coordinates.([][][]float64)[0]
	centerLon := (ring[0][0] + ring[2][0]) / 2
	centerLat := (ring[0][1] + ring[2][1]) / 2
	if math.Abs(centerLon-want[0]) > 1e-9 || math.Abs(centerLat-want[1]) > 1e-9 {
		t.Fatalf("fallback center = [%f,%f]; want %v", centerLon, centerLat, want)
	}
}

func TestFinalResultEventSerializesDataFreshness(t *testing.T) {
	event := FinalResultEvent{
		SuggestedAreasGeoJSON: map[string]any{"type": "FeatureCollection"},
		Objects:               []FinalResultObject{},
		DataFreshness:         "данные актуальны на 2026-07-14 12:00",
	}
	b, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["data_freshness"] != event.DataFreshness {
		t.Fatalf("data_freshness = %#v; want %q", got["data_freshness"], event.DataFreshness)
	}
}
