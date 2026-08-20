package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
)

// --- фейки для ResultsService (без реальной БД) ---------------------------

type fakeChatOwner struct {
	chat domain.Chat
	err  error
}

func (f fakeChatOwner) GetOwned(context.Context, uuid.UUID, uuid.UUID) (domain.Chat, error) {
	return f.chat, f.err
}

type fakeResultsLister struct {
	rows  []domain.ChatSearchResult
	total int
	err   error
}

func (f fakeResultsLister) ListResults(context.Context, uuid.UUID, int, int) ([]domain.ChatSearchResult, int, error) {
	return f.rows, f.total, f.err
}

type fakeListingLookup struct {
	byID map[string]domain.Listing
	err  error
}

func (f fakeListingLookup) GetByExternalIDs(context.Context, []string) (map[string]domain.Listing, error) {
	return f.byID, f.err
}

func TestResultsListRejectsForeignOrMissingChat(t *testing.T) {
	// Чужой/несуществующий чат — тот же приём и тот же код ответа, что и у
	// остальных ручек чата: ChatService.GetOwned мапит оба случая на 404
	// chat_not_found, никогда не 403 (см. chat_repo.go::GetOwned), — ResultsService
	// обязан пробросить эту же ошибку не подменяя её.
	svc := &ResultsService{chats: fakeChatOwner{err: apperr.ChatNotFound()}}

	_, _, _, err := svc.List(context.Background(), uuid.New(), uuid.New(), 10, 0)

	if err == nil {
		t.Fatal("err = nil; want chat_not_found")
	}
	appErr, ok := err.(*apperr.Error)
	if !ok || appErr.Code != "chat_not_found" {
		t.Fatalf("err = %#v; want *apperr.Error{Code: chat_not_found}", err)
	}
}

func TestResultsListReturnsStoredObjectsInFormatOfFinalResult(t *testing.T) {
	lon, lat := 37.6, 55.7
	price := int64(15_000_000)
	rooms := 2
	area := 45.0

	svc := &ResultsService{
		chats: fakeChatOwner{chat: domain.Chat{ID: uuid.New()}},
		results: fakeResultsLister{
			rows: []domain.ChatSearchResult{
				{ExternalID: "cian_1", MatchScore: 91, Price: &price, Rooms: &rooms, Area: &area},
			},
			total: 17, // сохранено больше, чем отдаём на этой странице
		},
		listings: fakeListingLookup{byID: map[string]domain.Listing{
			"cian_1": {ExternalID: "cian_1", Lon: &lon, Lat: &lat, Rooms: &rooms, Area: &area, Price: &price},
		}},
	}

	objects, count, total, err := svc.List(context.Background(), uuid.New(), uuid.New(), 10, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 17 {
		t.Fatalf("total = %d; want 17 (сколько всего в поиске)", total)
	}
	if count != 1 || len(objects) != 1 {
		t.Fatalf("count/len(objects) = %d/%d; want 1", count, len(objects))
	}
	if objects[0].ID != "cian_1" || objects[0].MatchScore != 91 {
		t.Fatalf("objects[0] = %#v", objects[0])
	}
	if objects[0].Coordinates[0] != lon || objects[0].Coordinates[1] != lat {
		t.Fatalf("координаты потерялись: %#v", objects[0].Coordinates)
	}
}

func TestResultsListSkipsObjectsMissingFromListings(t *testing.T) {
	// Объект деактивирован (пропал из listings) с момента сохранения строки
	// поиска — молча пропускаем карточку, как это уже делает final_result,
	// а не падаем и не выдумываем координаты.
	svc := &ResultsService{
		chats: fakeChatOwner{chat: domain.Chat{ID: uuid.New()}},
		results: fakeResultsLister{
			rows:  []domain.ChatSearchResult{{ExternalID: "gone"}},
			total: 1,
		},
		listings: fakeListingLookup{byID: map[string]domain.Listing{}},
	}

	objects, count, total, err := svc.List(context.Background(), uuid.New(), uuid.New(), 10, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if count != 0 || len(objects) != 0 {
		t.Fatalf("count/len(objects) = %d/%d; want 0", count, len(objects))
	}
	if total != 1 {
		t.Fatalf("total = %d; want 1 (total считает сохранённое, не отданное)", total)
	}
}

func TestResultsListEmptyWhenChatHasNoSearchYet(t *testing.T) {
	// Чат без единого поиска — ListResults репозитория отдаёт (nil, 0, nil),
	// сервис обязан вернуть пустой список, а не ошибку.
	svc := &ResultsService{
		chats:    fakeChatOwner{chat: domain.Chat{ID: uuid.New()}},
		results:  fakeResultsLister{rows: nil, total: 0},
		listings: fakeListingLookup{byID: map[string]domain.Listing{}},
	}

	objects, count, total, err := svc.List(context.Background(), uuid.New(), uuid.New(), 10, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if objects == nil || len(objects) != 0 {
		t.Fatalf("objects = %#v; want непустой (не nil) пустой срез", objects)
	}
	if count != 0 || total != 0 {
		t.Fatalf("count/total = %d/%d; want 0/0", count, total)
	}
}
