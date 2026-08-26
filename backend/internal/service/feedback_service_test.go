package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeFeedbackStore struct {
	saved domain.ResultFeedback
	calls int
}

func (f *fakeFeedbackStore) Upsert(_ context.Context, in domain.ResultFeedback) error {
	f.saved = in
	f.calls++
	return nil
}

type fakeResultGetter struct {
	err error
}

func (f fakeResultGetter) GetResult(context.Context, uuid.UUID, string) (domain.ChatSearchResult, error) {
	return domain.ChatSearchResult{}, f.err
}

func TestFeedbackSaveStoresVerdict(t *testing.T) {
	store := &fakeFeedbackStore{}
	svc := &FeedbackService{chats: fakeChatOwner{}, results: fakeResultGetter{}, feedback: store}
	userID, chatID := uuid.New(), uuid.New()

	if err := svc.Save(context.Background(), userID, chatID, "cian_1", "down", "далеко от метро"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if store.saved.Verdict != "down" || store.saved.Reason != "далеко от метро" {
		t.Fatalf("сохранено %+v", store.saved)
	}
	if store.saved.UserID != userID || store.saved.ChatID != chatID {
		t.Fatalf("контекст оценки потерян: %+v", store.saved)
	}
}

func TestFeedbackSaveRejectsUnknownVerdict(t *testing.T) {
	svc := &FeedbackService{chats: fakeChatOwner{}, results: fakeResultGetter{}, feedback: &fakeFeedbackStore{}}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "maybe", "")

	assertAppErrCode(t, err, "validation_error")
}

// Чужой чат — 404 chat_not_found, тот же приём, что у остальных ручек чата.
func TestFeedbackSaveRejectsForeignChat(t *testing.T) {
	svc := &FeedbackService{
		chats:    fakeChatOwner{err: apperr.ChatNotFound()},
		results:  fakeResultGetter{},
		feedback: &fakeFeedbackStore{},
	}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "up", "")

	assertAppErrCode(t, err, "chat_not_found")
}

// Оценивать объект, которого в этом подборе не было, нельзя: такая оценка —
// мусор в данных о качестве подбора.
func TestFeedbackSaveRejectsObjectOutsideChat(t *testing.T) {
	svc := &FeedbackService{
		chats:    fakeChatOwner{},
		results:  fakeResultGetter{err: repository.ErrNotFound},
		feedback: &fakeFeedbackStore{},
	}

	err := svc.Save(context.Background(), uuid.New(), uuid.New(), "cian_1", "up", "")

	assertAppErrCode(t, err, "object_not_found")
}
