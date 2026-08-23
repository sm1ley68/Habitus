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

// newExternalID возвращает уникальный external_id для теста. Литералы вроде
// "cian_319800087" ломают повторный ручной прогон против персистентной
// тестовой БД (testPool её не сбрасывает, как и session_repo_test.go для
// своих данных) — UNIQUE не пускает второй прогон. uuid.NewString() держит
// каждый прогон изолированным.
func newExternalID() string {
	return "cian_" + uuid.NewString()
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

func strptr(s string) *string { return &s }
