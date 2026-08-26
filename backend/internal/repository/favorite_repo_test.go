package repository

import (
	"context"
	"testing"
)

func TestFavoriteAddIsIdempotent(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()

	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("первое сохранение: %v", err)
	}
	// Повторный клик по «сохранить» — не ошибка: это то же состояние.
	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("повторное сохранение: %v", err)
	}

	rows, total, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d, ожидалось по 1", total, len(rows))
	}
}

func TestFavoriteAddKeepsChatContext(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	chats := NewChatRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	chat, err := chats.Create(ctx, userID, "msk", "Поиск")
	if err != nil {
		t.Fatalf("создать чат: %v", err)
	}
	externalID := newExternalID()

	if err := favs.Add(ctx, userID, externalID, &chat.ID); err != nil {
		t.Fatalf("Add: %v", err)
	}

	rows, _, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ChatID == nil || *rows[0].ChatID != chat.ID {
		t.Fatalf("chat_id не сохранился: %+v", rows)
	}
}

func TestFavoriteRemove(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()
	if err := favs.Add(ctx, userID, externalID, nil); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := favs.Remove(ctx, userID, externalID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Удаление отсутствующего — тоже не ошибка: состояние уже такое.
	if err := favs.Remove(ctx, userID, externalID); err != nil {
		t.Fatalf("повторное удаление: %v", err)
	}

	_, total, err := favs.List(ctx, userID, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, ожидался 0", total)
	}
}

func TestFavoriteListIsScopedToUser(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	favs := NewFavoriteRepo(pool)
	ctx := context.Background()

	mine := newTestUser(t, users)
	other := newTestUser(t, users)
	if err := favs.Add(ctx, mine, newExternalID(), nil); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, total, err := favs.List(ctx, other, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("чужому видно %d сохранённых", total)
	}
}
