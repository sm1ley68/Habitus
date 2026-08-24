package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func TestOwnerListingDTOShape(t *testing.T) {
	price := int64(12_500_000)
	area := float32(54.3)
	rooms, level, levels := 2, 4, 17
	lng, lat := 37.6595, 55.7108
	published := time.Date(2026, 8, 23, 10, 0, 0, 0, time.UTC)
	dto := OwnerListingDTO(domain.OwnerListing{
		ID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		ExternalID: "cian_318394906", Origin: "cian", Status: "published",
		Verification: "unverified", City: "msk",
		Price: &price, Area: &area, Rooms: &rooms, Level: &level, Levels: &levels,
		Address: "Москва, улица Мельникова, 3к1", Lng: &lng, Lat: &lat,
		Description: "Тихая двушка", Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		WindowOrientation: []string{"юг"},
		SourceURL:         "https://www.cian.ru/sale/flat/318394906/",
		PublishedAt:       &published,
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	coords, ok := got["coordinates"].([]any)
	if !ok || len(coords) != 2 {
		t.Fatalf("coordinates должны быть парой: %v", got["coordinates"])
	}
	// Контракт проекта: везде [lng, lat], без исключений.
	if coords[0].(float64) != 37.6595 || coords[1].(float64) != 55.7108 {
		t.Fatalf("порядок координат нарушен: %v", coords)
	}
	for _, key := range []string{"id", "external_id", "origin", "status",
		"verification", "city", "price", "area", "rooms", "level", "levels",
		"address", "description", "photos", "window_orientation", "source_url",
		"published_at", "updated_at"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
}

func TestOwnerListingDTONullsAreExplicit(t *testing.T) {
	dto := OwnerListingDTO(domain.OwnerListing{
		ID: uuid.New(), Status: "draft", Photos: []string{}, WindowOrientation: []string{},
	})
	raw, _ := json.Marshal(dto)
	var got map[string]any
	_ = json.Unmarshal(raw, &got)

	// Цена незаполненного черновика — null, а не 0: синтетический ноль вместо
	// отсутствующего значения запрещён правилами проекта.
	if got["price"] != nil {
		t.Fatalf("price = %v, ожидался null", got["price"])
	}
	if got["published_at"] != nil {
		t.Fatalf("published_at = %v, ожидался null", got["published_at"])
	}
	// То же и с координатами: черновик без точки на карте отдаёт null, а не
	// [0, 0] — иначе фронт нарисует метку в Гвинейском заливе.
	if got["coordinates"] != nil {
		t.Fatalf("coordinates = %v, ожидался null", got["coordinates"])
	}
	if photos, ok := got["photos"].([]any); !ok || photos == nil {
		t.Fatalf("photos должны быть пустым массивом, а не null: %v", got["photos"])
	}
}
