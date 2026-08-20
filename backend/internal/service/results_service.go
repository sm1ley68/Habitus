// results_service.go — GET /chats/{id}/results («показать ещё», Task 7):
// постраничный доступ к уже сохранённому пулу последнего поиска чата. Весь
// набор объектов из ответа ML уже лежит в chat_search_results (см.
// search_stream_service.go), поэтому запрос сюда не бьёт повторно в ML.
package service

import (
	"context"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// chatOwner — часть ChatService, нужная ResultsService: только проверка
// владения чатом. Обособленный интерфейс — чтобы в тестах подставить чужой/
// несуществующий чат без реальной БД (как chatSearchStore в
// search_stream_service.go).
type chatOwner interface {
	GetOwned(ctx context.Context, userID, chatID uuid.UUID) (domain.Chat, error)
}

// resultsLister — часть ChatSearchRepo, нужная ResultsService.
type resultsLister interface {
	ListResults(ctx context.Context, chatID uuid.UUID, limit, offset int) ([]domain.ChatSearchResult, int, error)
}

// listingLookup — часть ListingRepo, нужная ResultsService.
type listingLookup interface {
	GetByExternalIDs(ctx context.Context, ids []string) (map[string]domain.Listing, error)
}

type ResultsService struct {
	chats    chatOwner
	results  resultsLister
	listings listingLookup
}

func NewResultsService(chats *ChatService, results *repository.ChatSearchRepo,
	listings *repository.ListingRepo) *ResultsService {
	return &ResultsService{chats: chats, results: results, listings: listings}
}

// List отдаёт сохранённые объекты последнего поиска чата постранично, в том
// же формате объекта, что и final_result. count — сколько объектов реально
// попало в ответ (после отсева объектов, пропавших из listings), total —
// сколько всего сохранено для этого поиска (до пагинации).
func (s *ResultsService) List(ctx context.Context, userID, chatID uuid.UUID, limit, offset int) (objects []FinalResultObject, count, total int, err error) {
	if _, err := s.chats.GetOwned(ctx, userID, chatID); err != nil {
		return nil, 0, 0, err
	}

	rows, total, err := s.results.ListResults(ctx, chatID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}

	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ExternalID
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}

	objects = make([]FinalResultObject, 0, len(rows))
	for _, r := range rows {
		obj, ok := BuildStoredResultObject(r, listings)
		if !ok {
			continue // объект пропал из listings (деактивирован) — как в final_result
		}
		objects = append(objects, obj)
	}
	return objects, len(objects), total, nil
}
