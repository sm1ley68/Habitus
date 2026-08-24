package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeStore struct {
	items     map[uuid.UUID]domain.OwnerListing
	statusLog []string
	createErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{items: map[uuid.UUID]domain.OwnerListing{}}
}

func (f *fakeStore) put(l domain.OwnerListing) domain.OwnerListing {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	f.items[l.ID] = l
	return l
}

func (f *fakeStore) Create(_ context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	if f.createErr != nil {
		return domain.OwnerListing{}, f.createErr
	}
	l.Status, l.Verification = "draft", "unverified"
	return f.put(l), nil
}

func (f *fakeStore) GetOwned(_ context.Context, id, userID uuid.UUID) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	return l, nil
}

func (f *fakeStore) GetByExternalID(_ context.Context, externalID string) (domain.OwnerListing, error) {
	for _, l := range f.items {
		if l.ExternalID == externalID {
			return l, nil
		}
	}
	return domain.OwnerListing{}, repository.ErrNotFound
}

func (f *fakeStore) List(_ context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	out := []domain.OwnerListing{}
	for _, l := range f.items {
		if l.UserID == userID {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateFields(_ context.Context, id, userID uuid.UUID, fields domain.OwnerListingFields) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	if fields.Price != nil {
		l.Price = fields.Price
	}
	if fields.Description != nil {
		l.Description = *fields.Description
	}
	if fields.City != nil {
		l.City = *fields.City
	}
	f.items[id] = l
	return l, nil
}

func (f *fakeStore) SetPhotos(_ context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error) {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return domain.OwnerListing{}, repository.ErrNotFound
	}
	l.Photos = photos
	f.items[id] = l
	return l, nil
}

func (f *fakeStore) SetStatus(_ context.Context, id uuid.UUID, status, importError string) error {
	l, ok := f.items[id]
	if !ok {
		return repository.ErrNotFound
	}
	l.Status, l.ImportError = status, importError
	f.items[id] = l
	f.statusLog = append(f.statusLog, status)
	return nil
}

func (f *fakeStore) Delete(_ context.Context, id, userID uuid.UUID) error {
	l, ok := f.items[id]
	if !ok || l.UserID != userID {
		return repository.ErrNotFound
	}
	delete(f.items, id)
	return nil
}

type fakePublisher struct {
	upsertErr   error
	indexed     bool
	withdrawn   []string
	lastRequest client.OwnerUpsertRequest
}

func (p *fakePublisher) OwnerUpsert(_ context.Context, req client.OwnerUpsertRequest) (*client.OwnerUpsertResponse, error) {
	p.lastRequest = req
	if p.upsertErr != nil {
		return nil, p.upsertErr
	}
	return &client.OwnerUpsertResponse{ExternalID: req.ExternalID, Indexed: p.indexed}, nil
}

func (p *fakePublisher) OwnerWithdraw(_ context.Context, externalID string) (*client.OwnerWithdrawResponse, error) {
	p.withdrawn = append(p.withdrawn, externalID)
	return &client.OwnerWithdrawResponse{ExternalID: externalID, Deactivated: true}, nil
}

func publishableDraft(userID uuid.UUID) domain.OwnerListing {
	price := int64(12_000_000)
	area := float32(54.0)
	rooms, level, levels := 2, 4, 17
	lng, lat := 37.6055, 55.7601
	return domain.OwnerListing{
		UserID: userID, ExternalID: "owner_abc", Origin: "manual", Status: "draft",
		City: "msk", Price: &price, Area: &area, Rooms: &rooms, Level: &level, Levels: &levels,
		Address: "Москва, Тверская 1", Lng: &lng, Lat: &lat,
		Description: "Светлая двушка", Photos: []string{}, WindowOrientation: []string{},
	}
}

func TestCreateManualGeneratesOwnerExternalID(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{}, true)
	userID := uuid.New()

	created, err := svc.CreateManual(context.Background(), userID, publishableDraft(userID))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(created.ExternalID, "owner_") {
		t.Fatalf("external_id = %q, ожидался префикс owner_", created.ExternalID)
	}
	if created.Origin != "manual" || created.Status != "draft" {
		t.Fatalf("создано не как ручной черновик: %+v", created)
	}
}

func TestPublishSendsShowcasePayloadAndMarksPublished(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	published, err := svc.Publish(context.Background(), userID, draft.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if published.Status != "published" {
		t.Fatalf("статус = %q", published.Status)
	}
	if publisher.lastRequest.Source != "owner" || publisher.lastRequest.City != "msk" {
		t.Fatalf("на витрину ушло не то: %+v", publisher.lastRequest)
	}
	if publisher.lastRequest.Lng != 37.6055 {
		t.Fatalf("координаты не доехали: %+v", publisher.lastRequest)
	}
	// Промежуточный publishing обязателен: продавец видит, что идёт работа,
	// а не «ничего не произошло» на время расчёта эмбеддинга.
	if len(store.statusLog) < 2 || store.statusLog[0] != "publishing" {
		t.Fatalf("статусная машина: %+v", store.statusLog)
	}
}

func TestPublishRejectsListingWithoutCoordinates(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.Lng, draft.Lat = nil, nil
	stored := store.put(draft)

	_, err := svc.Publish(context.Background(), userID, stored.ID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "owner_listing_invalid" {
		t.Fatalf("ожидался owner_listing_invalid, получено %v", err)
	}
	if !strings.Contains(appErr.Message, "точку на карте") {
		t.Fatalf("сообщение должно объяснять, что делать: %q", appErr.Message)
	}
	// Отказ до смены статуса: черновик не должен застревать в publishing.
	if len(store.statusLog) != 0 {
		t.Fatalf("статус не должен был меняться: %+v", store.statusLog)
	}
	if publisher.lastRequest.ExternalID != "" {
		t.Fatal("витрину звать было незачем")
	}
}

func TestPublishOfImportedListingKeepsCianSource(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.ExternalID, draft.Origin = "cian_318394906", "cian"
	stored := store.put(draft)

	if _, err := svc.Publish(context.Background(), userID, stored.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if publisher.lastRequest.Source != "cian" {
		t.Fatalf("source = %q: у импортированного объявления источник остаётся cian",
			publisher.lastRequest.Source)
	}
}

func TestPublishFailureLeavesRecoverableState(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{upsertErr: errors.New("ml down")}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)
	if err == nil {
		t.Fatal("ошибка ML должна доезжать до продавца")
	}
	if store.items[draft.ID].Status != "failed" {
		t.Fatalf("статус = %q, ожидался failed", store.items[draft.ID].Status)
	}
	if store.items[draft.ID].ImportError == "" {
		t.Fatal("причина провала должна сохраняться — по ней рисуется кнопка «Повторить»")
	}
}

func TestPublishSurfacesValidationField(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{upsertErr: &client.OwnerListingInvalidError{
		Field: "coordinates", Message: "Координаты вне границ выбранного города"}}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "owner_listing_invalid" {
		t.Fatalf("ожидался owner_listing_invalid, получено %v", err)
	}
	if !strings.Contains(appErr.Message, "coordinates") {
		t.Fatalf("сообщение должно называть поле: %q", appErr.Message)
	}
}

func TestPublishRejectsNotIndexed(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{indexed: false}, true)
	userID := uuid.New()
	draft := store.put(publishableDraft(userID))

	_, err := svc.Publish(context.Background(), userID, draft.ID)
	if err == nil {
		t.Fatal("объект без эмбеддинга не находится поиском — это провал публикации")
	}
	if store.items[draft.ID].Status != "failed" {
		t.Fatalf("статус = %q", store.items[draft.ID].Status)
	}
}

func TestUnpublishWithdrawsFromShowcase(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.Status = "published"
	stored := store.put(draft)

	updated, err := svc.Unpublish(context.Background(), userID, stored.ID)
	if err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if updated.Status != "unpublished" {
		t.Fatalf("статус = %q", updated.Status)
	}
	if len(publisher.withdrawn) != 1 || publisher.withdrawn[0] != "owner_abc" {
		t.Fatalf("витрина не была уведомлена: %+v", publisher.withdrawn)
	}
}

func TestDeleteWithdrawsBeforeRemoving(t *testing.T) {
	store := newFakeStore()
	publisher := &fakePublisher{indexed: true}
	svc := NewOwnerListingService(store, publisher, true)
	userID := uuid.New()
	draft := publishableDraft(userID)
	draft.Status = "published"
	stored := store.put(draft)

	if err := svc.Delete(context.Background(), userID, stored.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(publisher.withdrawn) != 1 {
		t.Fatal("удаление опубликованного объявления обязано снять его с витрины")
	}
	if _, ok := store.items[stored.ID]; ok {
		t.Fatal("карточка должна быть удалена")
	}
}

func TestGetHidesForeignListing(t *testing.T) {
	store := newFakeStore()
	svc := NewOwnerListingService(store, &fakePublisher{}, true)
	stored := store.put(publishableDraft(uuid.New()))

	_, err := svc.Get(context.Background(), uuid.New(), stored.ID)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "owner_listing_not_found" {
		t.Fatalf("чужое объявление должно быть неотличимо от несуществующего, получено %v", err)
	}
}
