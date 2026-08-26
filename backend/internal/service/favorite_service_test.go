package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

type fakeFavoriteStore struct {
	rows  []domain.Favorite
	total int
}

func (f *fakeFavoriteStore) Add(context.Context, uuid.UUID, string, *uuid.UUID) error { return nil }
func (f *fakeFavoriteStore) Remove(context.Context, uuid.UUID, string) error          { return nil }
func (f *fakeFavoriteStore) List(context.Context, uuid.UUID, int, int) ([]domain.Favorite, int, error) {
	return f.rows, f.total, nil
}

// Хелперы указателей уже есть в пакете (owner_import_service_test.go):
// f64p, i64p, intp — свои не заводим, иначе в пакете окажется два набора
// имён для одного и того же.

// Карточка избранного собирается из фактов объекта. match_score и tags тут
// намеренно отсутствуют: они принадлежат запросу, и ноль вместо них был бы
// выдуманным «0% совпадения».
func TestBuildFavoriteObjectUsesListingFacts(t *testing.T) {
	chatID := uuid.New()
	addr := "Москва, улица Мельникова, 3к1"
	got, ok := BuildFavoriteObject(
		domain.Favorite{ExternalID: "cian_1", ChatID: &chatID, CreatedAt: time.Now()},
		domain.Listing{
			ExternalID: "cian_1", Lon: f64p(37.6595), Lat: f64p(55.7108),
			Address: &addr, Price: i64p(12_500_000), Rooms: intp(2), Area: f64p(54.3),
			Level: intp(4), Levels: intp(17),
			Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		})

	if !ok {
		t.Fatal("объект отброшен, хотя координаты есть")
	}
	if got.Address != addr {
		t.Fatalf("address = %q", got.Address)
	}
	if len(got.Coordinates) != 2 || got.Coordinates[0] != 37.6595 || got.Coordinates[1] != 55.7108 {
		t.Fatalf("координаты = %v, контракт проекта — [lng, lat]", got.Coordinates)
	}
	if got.Floor != "4/17" {
		t.Fatalf("floor = %q, ожидалось 4/17", got.Floor)
	}
	if got.CoverImage != "https://images.cdn-cian.ru/1.jpg" {
		t.Fatalf("cover_image = %q", got.CoverImage)
	}
	if got.ChatID == nil || *got.ChatID != chatID {
		t.Fatalf("chat_id потерян: %v", got.ChatID)
	}
}

// Объект без координат на карту не поставить — как и в выдаче, он отбрасывается.
func TestBuildFavoriteObjectSkipsListingWithoutCoordinates(t *testing.T) {
	if _, ok := BuildFavoriteObject(domain.Favorite{ExternalID: "cian_1"},
		domain.Listing{ExternalID: "cian_1"}); ok {
		t.Fatal("объект без координат попал в избранное")
	}
}

// Пропавший из витрины объект просто не показывается — 500 из-за него быть
// не должно, как и в выдаче результатов.
func TestFavoriteListSkipsMissingListings(t *testing.T) {
	store := &fakeFavoriteStore{
		rows:  []domain.Favorite{{ExternalID: "cian_gone"}, {ExternalID: "cian_here"}},
		total: 2,
	}
	addr := "Москва"
	svc := &FavoriteService{favorites: store, listings: fakeListingLookup{
		byID: map[string]domain.Listing{
			"cian_here": {ExternalID: "cian_here", Lon: f64p(37.6), Lat: f64p(55.7), Address: &addr},
		},
	}}

	objects, count, total, err := svc.List(context.Background(), uuid.New(), 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if count != 1 || len(objects) != 1 {
		t.Fatalf("count = %d, объектов = %d, ожидалось по 1", count, len(objects))
	}
	// total — сколько сохранено всего, до отсева: иначе «показать ещё» врёт.
	if total != 2 {
		t.Fatalf("total = %d, ожидалось 2", total)
	}
}
