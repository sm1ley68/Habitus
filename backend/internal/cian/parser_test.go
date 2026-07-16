package cian

import (
	"errors"
	"testing"
	"time"
)

func TestParseSearchResponse(t *testing.T) {
	t.Parallel()
	collectedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.FixedZone("test", 7*60*60))
	body := []byte(`{
  "data": {"offersSerialized": [
    {
      "cianId": 123456789,
      "description": "Светлая квартира рядом с парком",
      "bargainTerms": {"price": "18500000"},
      "totalArea": 54.7,
      "roomsCount": 2,
      "floorNumber": 7,
      "building": {"floorsCount": 12, "materialType": {"name": "Монолитный"}, "deadline": "2027"},
      "geo": {
        "userInput": "Москва, Ленинградский проспект, 10",
        "coordinates": {"lat": 55.789, "lng": 37.57},
        "undergrounds": [{"name": "Динамо", "time": 8, "transportType": "walk"}]
      },
      "newbuilding": {"name": "Прайм Парк"},
      "fullUrl": "https://www.cian.ru/sale/flat/123456789/"
    },
    {
      "id": "987",
      "description": "Второй оффер",
      "geo": {"address": [{"name": "Москва"}, {"fullName": "ул. Тверская, 1"}, {"name": "Москва"}]}
    },
    {"description": "no stable id"}
  ]}
}`)

	offers, err := ParseSearchResponse(body, collectedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 2 {
		t.Fatalf("len(offers) = %d; want 2", len(offers))
	}
	first := offers[0]
	if first.CianID != "123456789" || first.Description != "Светлая квартира рядом с парком" {
		t.Fatalf("unexpected identity fields: %#v", first)
	}
	if first.Price == nil || *first.Price != 18_500_000 || first.Area == nil || *first.Area != 54.7 {
		t.Fatalf("unexpected numeric fields: %#v", first)
	}
	if first.Rooms == nil || *first.Rooms != 2 || first.Floor == nil || *first.Floor != 7 || first.Floors == nil || *first.Floors != 12 {
		t.Fatalf("unexpected floor/room fields: %#v", first)
	}
	if first.BuildingMaterial != "Монолитный" || first.ResidentialComplex != "Прайм Парк" {
		t.Fatalf("unexpected building fields: %#v", first)
	}
	if len(first.Metro) != 1 || first.Metro[0].Name != "Динамо" || first.Metro[0].Time == nil || *first.Metro[0].Time != 8 {
		t.Fatalf("unexpected metro: %#v", first.Metro)
	}
	if first.CollectedAt.Location() != time.UTC || !first.CollectedAt.Equal(collectedAt) {
		t.Fatalf("collected_at = %v", first.CollectedAt)
	}
	if offers[1].Address != "Москва, ул. Тверская, 1" {
		t.Fatalf("fallback address = %q", offers[1].Address)
	}
}

func TestParseSearchResponseRequiresOffersArray(t *testing.T) {
	t.Parallel()
	_, err := ParseSearchResponse([]byte(`{"data":{}}`), time.Now())
	if !errors.Is(err, ErrMissingOffers) {
		t.Fatalf("error = %v; want ErrMissingOffers", err)
	}
}
