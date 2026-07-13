package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type ChatService struct {
	chats    *repository.ChatRepo
	messages *repository.MessageRepo
}

func NewChatService(chats *repository.ChatRepo, messages *repository.MessageRepo) *ChatService {
	return &ChatService{chats: chats, messages: messages}
}

func (s *ChatService) Create(ctx context.Context, userID uuid.UUID, city, title string) (domain.Chat, error) {
	if city != "spb" && city != "msk" {
		return domain.Chat{}, apperr.Validation("city must be 'spb' or 'msk'")
	}
	if title == "" {
		title = "Новый поиск квартиры"
	}
	return s.chats.Create(ctx, userID, city, title)
}

func (s *ChatService) List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Chat, int, error) {
	return s.chats.List(ctx, userID, limit, offset)
}

// GetOwned maps repository.ErrNotFound to the uniform 404 chat_not_found envelope.
func (s *ChatService) GetOwned(ctx context.Context, userID, chatID uuid.UUID) (domain.Chat, error) {
	chat, err := s.chats.GetOwned(ctx, chatID, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.Chat{}, apperr.ChatNotFound()
	}
	return chat, err
}

func (s *ChatService) Rename(ctx context.Context, userID, chatID uuid.UUID, title string) (domain.Chat, error) {
	if _, err := s.GetOwned(ctx, userID, chatID); err != nil {
		return domain.Chat{}, err
	}
	if title == "" {
		return domain.Chat{}, apperr.Validation("title is required")
	}
	return s.chats.Rename(ctx, chatID, title)
}

func (s *ChatService) Delete(ctx context.Context, userID, chatID uuid.UUID) error {
	if _, err := s.GetOwned(ctx, userID, chatID); err != nil {
		return err
	}
	return s.chats.Delete(ctx, chatID)
}

func (s *ChatService) Messages(ctx context.Context, userID, chatID uuid.UUID, limit, offset int) ([]domain.Message, int, error) {
	if _, err := s.GetOwned(ctx, userID, chatID); err != nil {
		return nil, 0, err
	}
	return s.messages.List(ctx, chatID, limit, offset)
}
