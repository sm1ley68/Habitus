package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func sampleListing(userID uuid.UUID, externalID string) domain.OwnerListing {
	price := int64(12_500_000)
	area := float32(54.3)
	rooms := 2
	lng := 37.6595
	lat := 55.7108
	return domain.OwnerListing{
		UserID:      userID,
		ExternalID:  externalID,
		Origin:      "cian",
		Status:      "draft",
		City:        "msk",
		Price:       &price,
		Area:        &area,
		Rooms:       &rooms,
		Address:     "Москва, улица Мельникова, 3к1",
		Lng:         &lng,
		Lat:         &lat,
		Description: "Тихая двушка окнами во двор",
		Photos:      []string{"https://images.cdn-cian.ru/images/1.jpg"},
		SourceURL:   "https://www.cian.ru/sale/flat/319800087/",
	}
}

func TestOwnerListingCreateAndGetOwned(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()
	externalID := newExternalID()

	created, err := repo.Create(ctx, sampleListing(userID, externalID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("ожидался сгенерированный id")
	}
	if created.Status != "draft" || created.Verification != "unverified" {
		t.Fatalf("дефолты не применились: %+v", created)
	}

	got, err := repo.GetOwned(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if got.ExternalID != externalID || *got.Price != 12_500_000 {
		t.Fatalf("неверные данные: %+v", got)
	}
}

// TestOwnerListingCreateWithoutCoordinatesKeepsNil — состояние «координат нет»
// должно остаться nil, а не подмениться синтетическим (0, 0) (Гвинейский
// залив). Правило «синтетический ноль вместо отсутствующего замера —
// запрещён» из CLAUDE.md. На практике это достижимо: у cian.Listing
// Latitude/Longitude — указатели, и объявление без координат придёт именно
// с nil.
func TestOwnerListingCreateWithoutCoordinatesKeepsNil(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	l := sampleListing(userID, newExternalID())
	l.Lng = nil
	l.Lat = nil

	created, err := repo.Create(ctx, l)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Lng != nil || created.Lat != nil {
		t.Fatalf("координаты без данных должны остаться nil, а не стать (0,0): %+v", created)
	}

	got, err := repo.GetOwned(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if got.Lng != nil || got.Lat != nil {
		t.Fatalf("координаты без данных должны читаться как nil: %+v", got)
	}
}

func TestOwnerListingGetOwnedHidesForeign(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.GetOwned(ctx, created.ID, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("чужой объект должен быть неотличим от несуществующего, получено %v", err)
	}
}

func TestOwnerListingExternalIDIsTaken(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	first, second := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	// Смысл теста — два пользователя приносят ОДИН И ТОТ ЖЕ external_id, поэтому
	// значение вычисляется один раз и передаётся в оба вызова.
	sharedExternalID := newExternalID()

	if _, err := repo.Create(ctx, sampleListing(first, sharedExternalID)); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := repo.Create(ctx, sampleListing(second, sharedExternalID))
	if !errors.Is(err, ErrExternalIDTaken) {
		t.Fatalf("ожидался ErrExternalIDTaken, получено %v", err)
	}
}

func TestOwnerListingGetByExternalID(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()
	externalID := newExternalID()

	created, err := repo.Create(ctx, sampleListing(userID, externalID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.GetByExternalID(ctx, externalID)
	if err != nil {
		t.Fatalf("get by external id: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("получен не тот объект: %+v", got)
	}

	if _, err := repo.GetByExternalID(ctx, newExternalID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound для несуществующего external_id, получено %v", err)
	}
}

func TestOwnerListingUpdateFields(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	newPrice := int64(11_000_000)
	updated, err := repo.UpdateFields(ctx, created.ID, userID, domain.OwnerListingFields{
		Price:       &newPrice,
		Description: strptr("Снизил цену"),
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if *updated.Price != 11_000_000 || updated.Description != "Снизил цену" {
		t.Fatalf("правка не применилась: %+v", updated)
	}
	if updated.Area == nil || *updated.Area != 54.3 {
		t.Fatalf("непереданные поля не должны обнуляться: %+v", updated)
	}
}

func TestOwnerListingUpdateFieldsForeignUserReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	newPrice := int64(1)
	if _, err := repo.UpdateFields(ctx, created.ID, stranger, domain.OwnerListingFields{Price: &newPrice}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}

	// Правка чужого не должна была пройти — цена должна остаться исходной.
	got, err := repo.GetOwned(ctx, created.ID, owner)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if *got.Price != 12_500_000 {
		t.Fatalf("цена не должна была измениться от чужой правки: %+v", got)
	}
}

func TestOwnerListingListIsScopedToUser(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	mine, other := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()
	mineExternalID := newExternalID()

	if _, err := repo.Create(ctx, sampleListing(mine, mineExternalID)); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, sampleListing(other, newExternalID())); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, mine)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ExternalID != mineExternalID {
		t.Fatalf("список должен содержать только свои объявления: %+v", list)
	}
}

func TestOwnerListingSetPhotosNilBecomesEmptyArray(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(created.Photos) == 0 {
		t.Fatal("sampleListing должен создавать объявление хотя бы с одним фото")
	}

	// nil — это, например, «удалили последнюю фотографию»: должен лечь как
	// пустой массив, а не уронить запрос NOT NULL-колонки.
	updated, err := repo.SetPhotos(ctx, created.ID, userID, nil)
	if err != nil {
		t.Fatalf("set photos nil: %v", err)
	}
	if updated.Photos == nil || len(updated.Photos) != 0 {
		t.Fatalf("photos должны стать пустым массивом, получено %+v", updated.Photos)
	}
}

func TestOwnerListingSetPhotosForeignUserReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.SetPhotos(ctx, created.ID, stranger, []string{"https://example.test/x.jpg"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
}

func TestOwnerListingSetStatusPublishedAtSetOnce(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.SetStatus(ctx, created.ID, "published", ""); err != nil {
		t.Fatalf("set status (первая публикация): %v", err)
	}
	first, err := repo.GetOwned(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if first.PublishedAt == nil {
		t.Fatal("published_at должен быть проставлен после первой публикации")
	}

	// Повторная публикация не должна переставлять published_at — это дата
	// появления в витрине, а не последней правки статуса.
	if err := repo.SetStatus(ctx, created.ID, "published", ""); err != nil {
		t.Fatalf("set status (повторная публикация): %v", err)
	}
	second, err := repo.GetOwned(ctx, created.ID, userID)
	if err != nil {
		t.Fatalf("get owned: %v", err)
	}
	if !second.PublishedAt.Equal(*first.PublishedAt) {
		t.Fatalf("повторная публикация переставила published_at: было %v, стало %v", first.PublishedAt, second.PublishedAt)
	}
}

func TestOwnerListingSetStatusMissingReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	ctx := context.Background()

	if err := repo.SetStatus(ctx, uuid.New(), "published", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound для несуществующего id, получено %v", err)
	}
}

func TestOwnerListingDeleteForeignUserReturnsNotFound(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, newExternalID()))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Delete(ctx, created.ID, stranger); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}

	if _, err := repo.GetOwned(ctx, created.ID, owner); err != nil {
		t.Fatalf("объект должен остаться на месте после чужой попытки удаления: %v", err)
	}
}
