package service

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type OwnerStore interface {
	Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error)
	GetOwned(ctx context.Context, id, userID uuid.UUID) (domain.OwnerListing, error)
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
	List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error)
	UpdateFields(ctx context.Context, id, userID uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error)
	SetPhotos(ctx context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error)
	SetStatus(ctx context.Context, id uuid.UUID, status, importError string) error
	Delete(ctx context.Context, id, userID uuid.UUID) error
}

type Publisher interface {
	OwnerUpsert(ctx context.Context, req client.OwnerUpsertRequest) (*client.OwnerUpsertResponse, error)
	OwnerWithdraw(ctx context.Context, externalID string) (*client.OwnerWithdrawResponse, error)
}

type OwnerListingService struct {
	store       OwnerStore
	publisher   Publisher
	autopublish bool
}

func NewOwnerListingService(store OwnerStore, publisher Publisher, autopublish bool) *OwnerListingService {
	return &OwnerListingService{store: store, publisher: publisher, autopublish: autopublish}
}

// Autopublish сообщает вызывающему, публиковать ли объявление сразу после
// импорта или создания. Рубильник живёт в сервисе, а не в хендлере: решение
// продуктовое, и хендлер о нём знать не должен.
func (s *OwnerListingService) Autopublish() bool { return s.autopublish }

func (s *OwnerListingService) List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	return s.store.List(ctx, userID)
}

func (s *OwnerListingService) Get(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	l, err := s.store.GetOwned(ctx, id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return l, err
}

// CreateManual заводит объявление, которого нет ни на Циане, ни в витрине.
// external_id синтезируется: у ручного объявления нет внешнего идентификатора,
// а витрине он нужен как единственный ключ объекта.
func (s *OwnerListingService) CreateManual(ctx context.Context, userID uuid.UUID, draft domain.OwnerListing) (domain.OwnerListing, error) {
	draft.UserID = userID
	draft.Origin = "manual"
	draft.ExternalID = "owner_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	draft.SourceURL = ""
	if draft.Photos == nil {
		draft.Photos = []string{}
	}
	if draft.WindowOrientation == nil {
		draft.WindowOrientation = []string{}
	}
	if err := validateCity(draft.City); err != nil {
		return domain.OwnerListing{}, err
	}

	created, err := s.store.Create(ctx, draft)
	if errors.Is(err, repository.ErrExternalIDTaken) {
		return domain.OwnerListing{}, apperr.Internal("не удалось выделить идентификатор объявления")
	}
	return created, err
}

func (s *OwnerListingService) Update(ctx context.Context, userID, id uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error) {
	if f.City != nil {
		if err := validateCity(*f.City); err != nil {
			return domain.OwnerListing{}, err
		}
	}
	updated, err := s.store.UpdateFields(ctx, id, userID, f)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return updated, err
}

func (s *OwnerListingService) SetPhotos(ctx context.Context, userID, id uuid.UUID, photos []string) (domain.OwnerListing, error) {
	updated, err := s.store.SetPhotos(ctx, id, userID, photos)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.OwnerListing{}, apperr.OwnerListingNotFound()
	}
	return updated, err
}

// Publish отдаёт объявление витрине и переводит статус.
//
// Промежуточный publishing выставляется до вызова ML: расчёт эмбеддинга
// занимает секунды, и без него продавец видит «ничего не произошло».
// Провал оставляет failed с причиной — расхождение кабинета и витрины видно
// продавцу и лечится кнопкой «Повторить», а не копится молча.
func (s *OwnerListingService) Publish(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	// Объект без координат публиковать некуда: витрина строит по ним гео-факты
	// и досье. Отвергаем здесь, до смены статуса, с указанием, что поправить —
	// подставить ноль значило бы опубликовать точку в Гвинейском заливе.
	if listing.Lng == nil || listing.Lat == nil {
		return domain.OwnerListing{}, apperr.OwnerListingInvalid(
			"coordinates", "Поставьте точку на карте — без неё объявление не опубликовать")
	}
	if err := s.store.SetStatus(ctx, id, "publishing", ""); err != nil {
		return domain.OwnerListing{}, err
	}

	source := "owner"
	if listing.Origin == "cian" {
		// У импортированного объявления источник остаётся cian: id стабилен,
		// и при следующем обходе краулер узнаёт объект как свой.
		source = "cian"
	}
	resp, err := s.publisher.OwnerUpsert(ctx, client.OwnerUpsertRequest{
		ExternalID: listing.ExternalID, Source: source, City: listing.City,
		Price: listing.Price, Area: listing.Area, KitchenArea: listing.KitchenArea,
		Rooms: listing.Rooms, Level: listing.Level, Levels: listing.Levels,
		Address: listing.Address, Lng: *listing.Lng, Lat: *listing.Lat,
		WindowOrientation: listing.WindowOrientation, Description: listing.Description,
		Photos: listing.Photos, SourceURL: listing.SourceURL,
	})

	var invalid *client.OwnerListingInvalidError
	switch {
	case errors.As(err, &invalid):
		_ = s.store.SetStatus(ctx, id, "failed", invalid.Message)
		return domain.OwnerListing{}, apperr.OwnerListingInvalid(invalid.Field, invalid.Message)
	case err != nil:
		_ = s.store.SetStatus(ctx, id, "failed", err.Error())
		cause, hint := mlDiagnosis(err)
		return domain.OwnerListing{}, apperr.
			Internal("Витрина не приняла объявление. Попробуйте ещё раз").
			WithCause(cause).WithHint(hint)
	case !resp.Indexed:
		// Объект без вектора лежит в базе и не находится семантическим
		// поиском — это хуже отсутствия, поэтому публикация считается провальной.
		_ = s.store.SetStatus(ctx, id, "failed", "объявление не проиндексировано")
		return domain.OwnerListing{}, apperr.Internal("Объявление не удалось проиндексировать. Попробуйте ещё раз")
	}

	if err := s.store.SetStatus(ctx, id, "published", ""); err != nil {
		return domain.OwnerListing{}, err
	}
	return s.Get(ctx, userID, id)
}

func (s *OwnerListingService) Unpublish(ctx context.Context, userID, id uuid.UUID) (domain.OwnerListing, error) {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	if _, err := s.publisher.OwnerWithdraw(ctx, listing.ExternalID); err != nil {
		cause, hint := mlDiagnosis(err)
		return domain.OwnerListing{}, apperr.
			Internal("Не удалось снять объявление с публикации").
			WithCause(cause).WithHint(hint)
	}
	if err := s.store.SetStatus(ctx, id, "unpublished", ""); err != nil {
		return domain.OwnerListing{}, err
	}
	return s.Get(ctx, userID, id)
}

// Delete сначала гасит объект в витрине и только потом удаляет карточку:
// обратный порядок оставил бы в поиске объявление, которым уже никто не владеет.
func (s *OwnerListingService) Delete(ctx context.Context, userID, id uuid.UUID) error {
	listing, err := s.Get(ctx, userID, id)
	if err != nil {
		return err
	}
	if listing.Status == "published" || listing.Status == "publishing" {
		if _, err := s.publisher.OwnerWithdraw(ctx, listing.ExternalID); err != nil {
			cause, hint := mlDiagnosis(err)
			return apperr.Internal("Не удалось снять объявление с публикации").
				WithCause(cause).WithHint(hint)
		}
	}
	err = s.store.Delete(ctx, id, userID)
	if errors.Is(err, repository.ErrNotFound) {
		return apperr.OwnerListingNotFound()
	}
	return err
}

func validateCity(city string) error {
	if city != "msk" && city != "spb" {
		return apperr.Validation("Город должен быть msk или spb")
	}
	return nil
}
