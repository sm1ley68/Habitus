// favorite_service.go — сохранённые объекты. До избранного объект жил только
// внутри чата: закрытая вкладка означала потерю находки.
package service

import (
	"context"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// favoriteStore — часть FavoriteRepo.
type favoriteStore interface {
	Add(ctx context.Context, userID uuid.UUID, externalID string, chatID *uuid.UUID) error
	Remove(ctx context.Context, userID uuid.UUID, externalID string) error
	List(ctx context.Context, userID uuid.UUID, limit, offset int) ([]domain.Favorite, int, error)
}

type FavoriteService struct {
	favorites favoriteStore
	listings  listingLookup
}

func NewFavoriteService(favorites *repository.FavoriteRepo, listings *repository.ListingRepo) *FavoriteService {
	return &FavoriteService{favorites: favorites, listings: listings}
}

// Add идемпотентен — намеренно не проверяет наличие объекта в витрине:
// проверка стоила бы похода в БД на каждый клик, а пропавший объект и так
// не попадёт в List.
func (s *FavoriteService) Add(ctx context.Context, userID uuid.UUID,
	externalID string, chatID *uuid.UUID) error {
	return s.favorites.Add(ctx, userID, externalID, chatID)
}

func (s *FavoriteService) Remove(ctx context.Context, userID uuid.UUID, externalID string) error {
	return s.favorites.Remove(ctx, userID, externalID)
}

// List. total — сколько сохранено всего, ДО отсева пропавших из витрины:
// иначе «показать ещё» врёт о размере списка (та же семантика, что у
// ResultsService.List).
func (s *FavoriteService) List(ctx context.Context, userID uuid.UUID,
	limit, offset int) (objects []FavoriteObject, count, total int, err error) {
	rows, total, err := s.favorites.List(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}

	ids := make([]string, len(rows))
	for i, f := range rows {
		ids[i] = f.ExternalID
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}

	objects = make([]FavoriteObject, 0, len(rows))
	for _, f := range rows {
		obj, ok := BuildFavoriteObject(f, listings[f.ExternalID])
		if !ok {
			continue
		}
		objects = append(objects, obj)
	}
	return objects, len(objects), total, nil
}
