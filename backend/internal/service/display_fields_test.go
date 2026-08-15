package service

import (
	"strings"
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

func TestBuildTagsDoesNotClaimMeasuredNoise(t *testing.T) {
	tags := BuildTags(map[string]any{"noise_level": "low", "bar_density_500m": 0.0})
	for _, tag := range tags {
		if strings.HasPrefix(tag, "шум:") {
			t.Fatalf("noise_level — прокси по барам, нельзя подавать как замер: %q", tag)
		}
	}
}

func TestBuildFinalResultObjectUsesFirstPhotoAsCover(t *testing.T) {
	lon, lat := 37.61, 55.75
	listings := map[string]domain.Listing{"A": {
		ExternalID: "A", Lon: &lon, Lat: &lat,
		Photos: []string{"https://cdn.cian.site/a.jpg", "https://cdn.cian.site/b.jpg"},
	}}
	obj, _ := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
	if obj.CoverImage != "https://cdn.cian.site/a.jpg" {
		t.Fatalf("обложка = %q; ждали первое фото", obj.CoverImage)
	}
}

func TestBuildFinalResultObjectFallsBackToPlaceholderWithoutPhotos(t *testing.T) {
	lon, lat := 37.61, 55.75
	for name, photos := range map[string][]string{"nil": nil, "пустой": {}} {
		listings := map[string]domain.Listing{"A": {ExternalID: "A", Lon: &lon, Lat: &lat, Photos: photos}}
		obj, _ := BuildFinalResultObject(client.ResultItem{ExternalID: "A"}, 0, nil, listings)
		if obj.CoverImage != PlaceholderCoverImage {
			t.Fatalf("%s: обложка = %q; ждали заглушку", name, obj.CoverImage)
		}
	}
}
