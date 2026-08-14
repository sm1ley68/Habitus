package service

import (
	"testing"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
)

func strp(s string) *string { return &s }

func TestBuildFinalResultObjectPrefersRealAddress(t *testing.T) {
	lon, lat := 37.61, 55.75
	listings := map[string]domain.Listing{"A": {
		ExternalID: "A", Lon: &lon, Lat: &lat,
		Address: strp("Москва, 2-й Донской проезд"),
	}}
	obj, ok := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
	if !ok {
		t.Fatal("объект должен собраться")
	}
	if obj.Address != "Москва, 2-й Донской проезд" {
		t.Fatalf("адрес не доехал: %q", obj.Address)
	}
}

func TestBuildFinalResultObjectFallsBackToSynthName(t *testing.T) {
	lon, lat := 37.61, 55.75
	rooms, area := 2, 54.0
	listings := map[string]domain.Listing{"A": {
		ExternalID: "A", Lon: &lon, Lat: &lat, Rooms: &rooms, Area: &area,
	}}
	obj, _ := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
	if obj.Address != "" {
		t.Fatalf("адреса нет — поле должно быть пустым, получено %q", obj.Address)
	}
	if obj.Name != "2-комн, 54 м²" {
		t.Fatalf("должен сработать SynthName, получено %q", obj.Name)
	}
}
