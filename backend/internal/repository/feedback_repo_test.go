package repository

import (
	"context"
	"testing"

	"habitus-backend/internal/domain"
)

// Оценку можно передумать: upsert, а не вставка. Иначе второй клик падал бы
// на первичном ключе, и пользователь застревал бы с первым вердиктом.
func TestFeedbackUpsertOverwritesVerdict(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	chats := NewChatRepo(pool)
	feedback := NewFeedbackRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	chat, err := chats.Create(ctx, userID, "msk", "Поиск")
	if err != nil {
		t.Fatalf("создать чат: %v", err)
	}
	externalID := newExternalID()

	if err := feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chat.ID, ExternalID: externalID,
		Verdict: "down", Reason: "далеко от метро",
	}); err != nil {
		t.Fatalf("первая оценка: %v", err)
	}
	if err := feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chat.ID, ExternalID: externalID, Verdict: "up",
	}); err != nil {
		t.Fatalf("вторая оценка: %v", err)
	}

	var verdict, reason string
	err = pool.QueryRow(ctx, `
		SELECT verdict, reason FROM result_feedback
		WHERE user_id = $1 AND chat_id = $2 AND external_id = $3`,
		userID, chat.ID, externalID).Scan(&verdict, &reason)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if verdict != "up" {
		t.Fatalf("verdict = %q, ожидался up", verdict)
	}
	// Причина от прошлого вердикта не должна прилипать к новому.
	if reason != "" {
		t.Fatalf("reason = %q, ожидалась пустая", reason)
	}
}
