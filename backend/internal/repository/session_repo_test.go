package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedUser создаёт пользователя — sessions.user_id ссылается на users.
func seedUser(t *testing.T, repo *UserRepo) uuid.UUID {
	t.Helper()
	email := "sweep-" + uuid.NewString() + "@example.com"
	u, err := repo.Create(context.Background(), email, "hash", "Тест")
	if err != nil {
		t.Fatalf("создание пользователя: %v", err)
	}
	return u.ID
}

func TestDeleteExpiredRemovesOnlyStaleSessions(t *testing.T) {
	// Протухшие сессии не удалял никто: GetSession их просто не отдаёт, а строки
	// копятся в таблице вечно.
	pool := testPool(t)
	ctx := context.Background()
	sessions := NewSessionRepo(pool)
	userID := seedUser(t, NewUserRepo(pool))

	live := "живой-" + uuid.NewString()
	stale := "протухший-" + uuid.NewString()
	if err := sessions.Create(ctx, live, userID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Create(live): %v", err)
	}
	if err := sessions.Create(ctx, stale, userID, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("Create(stale): %v", err)
	}

	removed, err := sessions.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired() error = %v", err)
	}
	if removed < 1 {
		t.Fatalf("removed = %d; протухшая сессия должна быть удалена", removed)
	}

	if _, _, err := sessions.GetSession(ctx, live); err != nil {
		t.Fatalf("живая сессия пропала после чистки: %v", err)
	}

	// Повторный прогон по той же таблице уже ничего не находит — значит
	// удалили именно протухшее, а не «что-нибудь».
	again, err := sessions.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired() второй раз: %v", err)
	}
	if again != 0 {
		t.Fatalf("removed = %d при повторной чистке; want 0", again)
	}
}
