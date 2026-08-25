package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateGuestHasNoCredentials(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)

	guest, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	if !guest.IsGuest {
		t.Fatal("IsGuest = false у только что созданного гостя")
	}
	if guest.Email != "" {
		t.Fatalf("Email = %q, у гостя его быть не должно", guest.Email)
	}
	if guest.ID == uuid.Nil {
		t.Fatal("ID пустой")
	}
}

// Два гостя подряд — законный сценарий (два браузера). Уникальность email
// не должна им мешать: в Postgres UNIQUE пропускает сколько угодно NULL.
func TestCreateGuestTwiceSucceeds(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)

	first, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("первый гость: %v", err)
	}
	second, err := repo.CreateGuest(context.Background())
	if err != nil {
		t.Fatalf("второй гость: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("оба гостя получили один id")
	}
}

// Ключевое свойство схемы: апгрейд сохраняет id, поэтому всё, что гость успел
// сделать (чаты, результаты, избранное), остаётся при нём после регистрации.
func TestUpgradeGuestKeepsSameID(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	email := uuid.NewString() + "@example.test"

	upgraded, err := repo.UpgradeGuest(ctx, guest.ID, email, "hash", "Покупатель")
	if err != nil {
		t.Fatalf("UpgradeGuest: %v", err)
	}
	if upgraded.ID != guest.ID {
		t.Fatalf("id сменился: %s → %s", guest.ID, upgraded.ID)
	}
	if upgraded.IsGuest {
		t.Fatal("IsGuest = true после апгрейда")
	}
	if upgraded.Email != email {
		t.Fatalf("Email = %q, ожидался %q", upgraded.Email, email)
	}
}

func TestUpgradeGuestRejectsTakenEmail(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	email := uuid.NewString() + "@example.test"
	if _, err := repo.Create(ctx, email, "hash", "Занявший"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}

	_, err = repo.UpgradeGuest(ctx, guest.ID, email, "hash", "Гость")

	if err != ErrDuplicateEmail {
		t.Fatalf("err = %v, ожидался ErrDuplicateEmail", err)
	}
}

// Зарегистрированного апгрейдить нельзя: иначе чужой email перезаписал бы
// существующий аккаунт.
func TestUpgradeGuestRejectsRegisteredUser(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	user, err := repo.Create(ctx, uuid.NewString()+"@example.test", "hash", "Аккаунт")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.UpgradeGuest(ctx, user.ID, uuid.NewString()+"@example.test", "hash", "Новый")

	if err != ErrNotFound {
		t.Fatalf("err = %v, ожидался ErrNotFound", err)
	}
}

// Свежего гостя чистка не трогает: он прямо сейчас ищет квартиру.
func TestDeleteStaleGuestsSparesFreshOnes(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}

	if _, err := repo.DeleteStaleGuests(ctx, 24*time.Hour); err != nil {
		t.Fatalf("DeleteStaleGuests: %v", err)
	}

	if _, err := repo.GetByID(ctx, guest.ID); err != nil {
		t.Fatalf("свежего гостя удалили: %v", err)
	}
}

// Нулевой возраст означает «всех гостей», зарегистрированных при этом не
// трогает — проверяем обе половины одним прогоном.
func TestDeleteStaleGuestsRemovesGuestsOnly(t *testing.T) {
	pool := testPool(t)
	repo := NewUserRepo(pool)
	ctx := context.Background()

	guest, err := repo.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	user, err := repo.Create(ctx, uuid.NewString()+"@example.test", "hash", "Аккаунт")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.DeleteStaleGuests(ctx, 0); err != nil {
		t.Fatalf("DeleteStaleGuests: %v", err)
	}

	if _, err := repo.GetByID(ctx, guest.ID); err != ErrNotFound {
		t.Fatalf("гость уцелел: err = %v", err)
	}
	if _, err := repo.GetByID(ctx, user.ID); err != nil {
		t.Fatalf("удалили зарегистрированного: %v", err)
	}
}

func TestGetSessionReportsGuestFlag(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	sessions := NewSessionRepo(pool)
	ctx := context.Background()

	guest, err := users.CreateGuest(ctx)
	if err != nil {
		t.Fatalf("CreateGuest: %v", err)
	}
	token := uuid.NewString()
	if err := sessions.Create(ctx, token, guest.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	userID, isGuest, err := sessions.GetSession(ctx, token)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if userID != guest.ID {
		t.Fatalf("user_id = %s, ожидался %s", userID, guest.ID)
	}
	if !isGuest {
		t.Fatal("is_guest = false у сессии гостя")
	}
}

func TestGetSessionRejectsExpired(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	sessions := NewSessionRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	token := uuid.NewString()
	if err := sessions.Create(ctx, token, userID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("Create session: %v", err)
	}

	if _, _, err := sessions.GetSession(ctx, token); err != ErrNotFound {
		t.Fatalf("err = %v, ожидался ErrNotFound на протухшей сессии", err)
	}
}
