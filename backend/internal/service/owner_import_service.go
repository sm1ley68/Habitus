package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/cian"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// Вердикты предпросмотра — три взаимоисключающих состояния самого объявления.
// Похожие объекты ортогональны вердикту и едут отдельным полем: новое
// объявление вполне может иметь похожего соседа.
const (
	VerdictNew          = "new"
	VerdictClaimable    = "claimable"
	VerdictAlreadyYours = "already_yours"
)

type OfferFetcher interface {
	FetchByID(ctx context.Context, offerID int64) (cian.Listing, error)
}

type OwnerListingsRepo interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
	Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error)
}

// ShowcaseRepo — чтение витрины. Координаты указателями: у строки витрины
// geom может отсутствовать, и «координат нет» обязано доезжать сюда как nil,
// а не как точка [0, 0].
type ShowcaseRepo interface {
	SnapshotByExternalID(ctx context.Context, externalID string) (domain.ListingSnapshot, error)
	FindSimilar(ctx context.Context, lng, lat *float64, rooms, level *int, area *float32, excludeExternalID string) ([]domain.SimilarListing, error)
}

type ImportPreview struct {
	Verdict    string
	Draft      domain.OwnerListing
	Similar    []domain.SimilarListing
	ExistingID *uuid.UUID
}

type OwnerImportService struct {
	owners   OwnerListingsRepo
	showcase ShowcaseRepo
	fetcher  OfferFetcher
	limiter  *cian.RateLimiter
	quota    *cian.UserQuota
}

func NewOwnerImportService(owners OwnerListingsRepo, showcase ShowcaseRepo,
	fetcher OfferFetcher, limiter *cian.RateLimiter, quota *cian.UserQuota) *OwnerImportService {
	return &OwnerImportService{owners: owners, showcase: showcase,
		fetcher: fetcher, limiter: limiter, quota: quota}
}

// Preview разбирает ссылку и возвращает то, что продавец увидит до импорта.
// Проверки идут от дешёвых к дорогим: свой кабинет → чужой кабинет → витрина →
// и только потом Циан. На собранной базе большинство ссылок закрывается
// третьим шагом, без единого исходящего запроса.
func (s *OwnerImportService) Preview(ctx context.Context, userID uuid.UUID, rawURL string) (ImportPreview, error) {
	offerID, err := cian.ParseOfferURL(rawURL)
	if err != nil {
		return ImportPreview{}, apperr.CianURLInvalid()
	}
	externalID := fmt.Sprintf("cian_%d", offerID)

	existing, err := s.owners.GetByExternalID(ctx, externalID)
	switch {
	case err == nil && existing.UserID == userID:
		id := existing.ID
		return ImportPreview{Verdict: VerdictAlreadyYours, Draft: existing, ExistingID: &id}, nil
	case err == nil:
		return ImportPreview{}, apperr.ListingClaimedByOther()
	case !errors.Is(err, repository.ErrNotFound):
		return ImportPreview{}, err
	}

	draft, err := s.draftFromShowcase(ctx, externalID)
	verdict := VerdictClaimable
	if errors.Is(err, repository.ErrNotFound) {
		draft, err = s.draftFromCian(ctx, userID, offerID, externalID)
		verdict = VerdictNew
	}
	if err != nil {
		return ImportPreview{}, err
	}
	draft.UserID = userID

	similar, err := s.showcase.FindSimilar(ctx, draft.Lng, draft.Lat,
		draft.Rooms, draft.Level, draft.Area, externalID)
	if err != nil {
		return ImportPreview{}, err
	}
	return ImportPreview{Verdict: verdict, Draft: draft, Similar: similar}, nil
}

// Import выполняет то же ветвление и создаёт карточку в кабинете.
func (s *OwnerImportService) Import(ctx context.Context, userID uuid.UUID, rawURL string) (domain.OwnerListing, error) {
	preview, err := s.Preview(ctx, userID, rawURL)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	if preview.Verdict == VerdictAlreadyYours {
		return preview.Draft, nil
	}

	created, err := s.owners.Create(ctx, preview.Draft)
	if errors.Is(err, repository.ErrExternalIDTaken) {
		// Гонка двух одновременных импортов одной ссылки: проверку выше прошли
		// оба, вставку выиграл один. UNIQUE — единственная надёжная арбитражная
		// точка, поэтому конфликт разбирается здесь, а не предварительной блокировкой.
		return domain.OwnerListing{}, apperr.ListingClaimedByOther()
	}
	return created, err
}

func (s *OwnerImportService) draftFromShowcase(ctx context.Context, externalID string) (domain.OwnerListing, error) {
	snapshot, err := s.showcase.SnapshotByExternalID(ctx, externalID)
	if err != nil {
		return domain.OwnerListing{}, err
	}
	return domain.OwnerListing{
		ExternalID: snapshot.ExternalID, Origin: "cian", City: snapshot.City,
		Price: snapshot.Price, Area: snapshot.Area, KitchenArea: snapshot.KitchenArea,
		Rooms: snapshot.Rooms, Level: snapshot.Level, Levels: snapshot.Levels,
		Address: snapshot.Address, Lng: snapshot.Lng, Lat: snapshot.Lat,
		WindowOrientation: snapshot.WindowOrientation, Description: snapshot.Description,
		Photos: snapshot.Photos, SourceURL: snapshot.SourceURL,
	}, nil
}

func (s *OwnerImportService) draftFromCian(ctx context.Context, userID uuid.UUID,
	offerID int64, externalID string) (domain.OwnerListing, error) {
	if !s.quota.Allow(userID.String()) {
		return domain.OwnerListing{}, apperr.RateLimited(
			"Слишком много импортов за час. Попробуйте позже")
	}
	if !s.limiter.Allow() {
		return domain.OwnerListing{}, apperr.CianUnavailable()
	}

	listing, err := s.fetcher.FetchByID(ctx, offerID)
	switch {
	case errors.Is(err, cian.ErrOfferNotFound):
		return domain.OwnerListing{}, apperr.CianOfferNotFound()
	case errors.Is(err, cian.ErrBlocked):
		return domain.OwnerListing{}, apperr.CianUnavailable()
	case err != nil:
		return domain.OwnerListing{}, apperr.CianUnavailable()
	}
	return listingToDraft(listing, externalID), nil
}

// listingToDraft переводит модель парсера в карточку кабинета. Циан отдаёт
// площадь float64, витрина хранит real — сужение здесь, а не в репозитории,
// чтобы преобразование было видно в одном месте.
func listingToDraft(l cian.Listing, externalID string) domain.OwnerListing {
	draft := domain.OwnerListing{
		ExternalID: externalID, Origin: "cian", City: "msk",
		Price: l.Price, Rooms: l.Rooms, Level: l.Floor, Levels: l.Floors,
		Address: strings.TrimSpace(l.Address), Description: strings.TrimSpace(l.Description),
		Photos: l.Photos, SourceURL: l.URL,
	}
	if l.Area != nil {
		area := float32(*l.Area)
		draft.Area = &area
	}
	// Указатели переносятся как есть: у объявления Циана координат может не быть,
	// и это должно остаться видимым, а не превратиться в точку [0, 0].
	draft.Lng = l.Longitude
	draft.Lat = l.Latitude
	if draft.Photos == nil {
		draft.Photos = []string{}
	}
	if draft.WindowOrientation == nil {
		draft.WindowOrientation = []string{}
	}
	return draft
}
