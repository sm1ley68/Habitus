package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func newTestUser(t *testing.T, r *UserRepo) uuid.UUID {
	t.Helper()
	u, err := r.Create(context.Background(), uuid.NewString()+"@example.test", "hash", "Продавец")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func sampleListing(userID uuid.UUID, externalID string) domain.OwnerListing {
	price := int64(12_500_000)
	area := float32(54.3)
	rooms := 2
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
		Lng:         37.6595,
		Lat:         55.7108,
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

	created, err := repo.Create(ctx, sampleListing(userID, "cian_319800087"))
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
	if got.ExternalID != "cian_319800087" || *got.Price != 12_500_000 {
		t.Fatalf("неверные данные: %+v", got)
	}
}

func TestOwnerListingGetOwnedHidesForeign(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	owner, stranger := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(owner, "cian_1001"))
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

	if _, err := repo.Create(ctx, sampleListing(first, "cian_2002")); err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err := repo.Create(ctx, sampleListing(second, "cian_2002"))
	if !errors.Is(err, ErrExternalIDTaken) {
		t.Fatalf("ожидался ErrExternalIDTaken, получено %v", err)
	}
}

func TestOwnerListingUpdateFields(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	userID := newTestUser(t, NewUserRepo(pool))
	ctx := context.Background()

	created, err := repo.Create(ctx, sampleListing(userID, "cian_3003"))
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

func TestOwnerListingListIsScopedToUser(t *testing.T) {
	pool := testPool(t)
	repo := NewOwnerListingRepo(pool)
	users := NewUserRepo(pool)
	mine, other := newTestUser(t, users), newTestUser(t, users)
	ctx := context.Background()

	if _, err := repo.Create(ctx, sampleListing(mine, "cian_4004")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.Create(ctx, sampleListing(other, "cian_5005")); err != nil {
		t.Fatalf("create: %v", err)
	}

	list, err := repo.List(ctx, mine)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ExternalID != "cian_4004" {
		t.Fatalf("список должен содержать только свои объявления: %+v", list)
	}
}

func strptr(s string) *string { return &s }
