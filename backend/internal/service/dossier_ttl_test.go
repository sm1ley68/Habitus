package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// --- срок жизни кэша досье (Task 7) --------------------------------------

func TestDossierFreshWithinTTLAndNoListingUpdate(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-2 * time.Hour)

	if !dossierFresh(&dossierAt, nil, 24, now) {
		t.Fatal("кэш моложе TTL и без обновления объекта должен считаться свежим")
	}
}

func TestDossierStaleWhenOlderThanTTL(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-25 * time.Hour) // TTL = 24ч

	if dossierFresh(&dossierAt, nil, 24, now) {
		t.Fatal("кэш старше TTL должен считаться протухшим")
	}
}

func TestDossierStaleWhenListingUpdatedAfterDossier(t *testing.T) {
	// Досье моложе TTL, но объект в listings обновился циклом сбора уже после
	// того, как досье было посчитано, — кэш обязан протухнуть.
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-1 * time.Hour)
	listingAt := now.Add(-30 * time.Minute) // новее dossierAt

	if dossierFresh(&dossierAt, &listingAt, 24, now) {
		t.Fatal("досье старше listings.updated_at должно считаться протухшим")
	}
}

func TestDossierFreshWhenListingOlderThanDossier(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-1 * time.Hour)
	listingAt := now.Add(-3 * time.Hour) // старше dossierAt — объект не менялся с момента расчёта

	if !dossierFresh(&dossierAt, &listingAt, 24, now) {
		t.Fatal("объект не обновлялся после расчёта досье — кэш должен остаться свежим")
	}
}

func TestDossierStaleWhenNoCacheYet(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if dossierFresh(nil, nil, 24, now) {
		t.Fatal("без dossier_updated_at кэша ещё нет — не может быть свежим")
	}
}

// --- деградация: протухший кэш лучше пустоты (финальное ревью ветки) --------

type fakeDossierStore struct {
	result     domain.ChatSearchResult
	search     domain.ChatSearch
	searchErr  error
	savedCalls int
}

func (f *fakeDossierStore) GetResult(context.Context, uuid.UUID, string) (domain.ChatSearchResult, error) {
	return f.result, nil
}
func (f *fakeDossierStore) GetSearch(context.Context, uuid.UUID) (domain.ChatSearch, error) {
	return f.search, f.searchErr
}
func (f *fakeDossierStore) SaveDossier(context.Context, uuid.UUID, uuid.UUID,
	string, string, map[string]any) error {
	f.savedCalls++
	return nil
}

type fakeListingSource struct{ updatedAt *time.Time }

func (f fakeListingSource) GetByExternalID(context.Context, string) (domain.Listing, error) {
	return domain.Listing{}, nil
}
func (f fakeListingSource) GetUpdatedAt(context.Context, string) (*time.Time, error) {
	return f.updatedAt, nil
}

// staleResult — строка с досье, протухшим по TTL: сутки TTL, досье двухдневное.
func staleResult() domain.ChatSearchResult {
	old := time.Now().Add(-48 * time.Hour)
	return domain.ChatSearchResult{
		ExternalID:       "E1",
		SearchID:         uuid.New(),
		DossierVersion:   DossierSchemaVersion,
		DossierUpdatedAt: &old,
		Dossier: map[string]any{
			"verdict":        map[string]any{"headline": "устаревшее досье", "confidence": 1, "layers_checked": 1},
			"brief":          []any{},
			"blocks":         []any{},
			"compromises":    []any{},
			"relaxation":     []any{},
			"zone_rationale": "",
		},
	}
}

func TestStaleDossierServedWhenMLIsDown(t *testing.T) {
	// Обмен «слегка устаревшее досье» на «блока нет вовсе» был бы ухудшением
	// деградации: объекты уже показаны, и терять по ним досье из-за упавшей ML
	// пользователю хуже, чем увидеть вчерашние цифры.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	res := staleResult()
	svc := &ObjectService{
		results:   &fakeDossierStore{result: res, search: domain.ChatSearch{ID: res.SearchID}},
		listings:  fakeListingSource{},
		ml:        client.NewMLClient(down.URL, time.Second),
		mlTimeout: time.Second,
		ttlHours:  24,
		inFlight:  make(map[string]*dossierCall),
	}

	payload, ok := svc.dossier(context.Background(), uuid.New(), "E1", "msk", res)

	if !ok {
		t.Fatal("ok = false; протухший кэш обязан пережить отказ ML")
	}
	if payload.Verdict.Headline != "устаревшее досье" {
		t.Fatalf("Headline = %q; ожидалось содержимое кэша", payload.Verdict.Headline)
	}
}

func TestStaleDossierServedWhenSearchContextIsGone(t *testing.T) {
	// Второй путь отказа: контекст исходного поиска не читается — запрос к ML
	// собрать не из чего, но кэш по-прежнему годен показать.
	res := staleResult()
	svc := &ObjectService{
		results:   &fakeDossierStore{result: res, searchErr: repository.ErrNotFound},
		listings:  fakeListingSource{},
		ml:        client.NewMLClient("http://127.0.0.1:1", time.Second),
		mlTimeout: time.Second,
		ttlHours:  24,
		inFlight:  make(map[string]*dossierCall),
	}

	payload, ok := svc.dossier(context.Background(), uuid.New(), "E1", "msk", res)

	if !ok || payload.Verdict.Headline != "устаревшее досье" {
		t.Fatalf("payload=%q ok=%v; кэш обязан пережить потерю контекста поиска",
			payload.Verdict.Headline, ok)
	}
}

func TestNoCacheAndMLDownYieldsNothing(t *testing.T) {
	// Обратная сторона: кэша нет вовсе — подменять его нечем, отдаём пусто.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer down.Close()

	res := domain.ChatSearchResult{ExternalID: "E2", SearchID: uuid.New()}
	svc := &ObjectService{
		results:   &fakeDossierStore{result: res, search: domain.ChatSearch{ID: res.SearchID}},
		listings:  fakeListingSource{},
		ml:        client.NewMLClient(down.URL, time.Second),
		mlTimeout: time.Second,
		ttlHours:  24,
		inFlight:  make(map[string]*dossierCall),
	}

	if _, ok := svc.dossier(context.Background(), uuid.New(), "E2", "msk", res); ok {
		t.Fatal("ok = true; без кэша и без ML отдавать нечего")
	}
}
