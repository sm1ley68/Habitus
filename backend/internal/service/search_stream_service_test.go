package service

import "testing"

func TestPickSuggestedAreas(t *testing.T) {
	hull := map[string]any{"type": "FeatureCollection", "features": []any{"hull"}}
	zone := map[string]any{"type": "FeatureCollection", "features": []any{"zone"}}

	// зона есть → она вытесняет hull
	if got := pickSuggestedAreas(hull, zone); got == nil ||
		got.(map[string]any)["features"].([]any)[0] != "zone" {
		t.Fatalf("зона должна заменить hull, получили %v", got)
	}
	// зоны нет → остаётся hull
	if got := pickSuggestedAreas(hull, nil); got.(map[string]any)["features"].([]any)[0] != "hull" {
		t.Fatalf("без зоны должен остаться hull, получили %v", got)
	}
}
