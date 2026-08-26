// feedback_service.go — оценка объекта в выдаче. Единственный продакшн-сигнал
// о том, работает ли подбор: eval меряет качество на golden-set оффлайн, а
// что думают живые люди, до этого было неизвестно.
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

const feedbackReasonMaxLen = 500

// resultGetter — часть ChatSearchRepo: убедиться, что объект вообще был в
// этом подборе.
type resultGetter interface {
	GetResult(ctx context.Context, chatID uuid.UUID, externalID string) (domain.ChatSearchResult, error)
}

// feedbackStore — часть FeedbackRepo.
type feedbackStore interface {
	Upsert(ctx context.Context, f domain.ResultFeedback) error
}

type FeedbackService struct {
	chats    chatOwner
	results  resultGetter
	feedback feedbackStore
}

func NewFeedbackService(chats *ChatService, results *repository.ChatSearchRepo,
	feedback *repository.FeedbackRepo) *FeedbackService {
	return &FeedbackService{chats: chats, results: results, feedback: feedback}
}

func (s *FeedbackService) Save(ctx context.Context, userID, chatID uuid.UUID,
	externalID, verdict, reason string) error {
	if verdict != "up" && verdict != "down" {
		return apperr.Validation("verdict должен быть 'up' или 'down'")
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > feedbackReasonMaxLen {
		return apperr.Validation("Слишком длинное объяснение оценки")
	}

	if _, err := s.chats.GetOwned(ctx, userID, chatID); err != nil {
		return err
	}
	// Объект должен быть в выдаче этого чата: оценка объекта, которого тут не
	// показывали, — мусор в данных о качестве подбора.
	if _, err := s.results.GetResult(ctx, chatID, externalID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return apperr.ObjectNotFound()
		}
		return err
	}

	return s.feedback.Upsert(ctx, domain.ResultFeedback{
		UserID: userID, ChatID: chatID, ExternalID: externalID,
		Verdict: verdict, Reason: reason,
	})
}
