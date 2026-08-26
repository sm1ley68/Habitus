// lead_service.go — заявка покупателя продавцу. Это единственное место, где
// путь пользователя из «нашли квартиру» переходит в «договорились о просмотре»:
// до его появления паспорт был тупиком.
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

// Границы полей заявки. Не про безопасность (BodyLimit уже есть), а про то,
// что продавец должен прочитать заявку, а не простыню.
const (
	leadNameMaxLen    = 120
	leadContactMaxLen = 200
	leadMessageMaxLen = 1000
)

// LeadInput — то, что покупатель сообщает о себе. Контакт продавца в обратную
// сторону не уходит: связь идёт через кабинет.
type LeadInput struct {
	Name    string
	Contact string
	Message string
}

// leadTarget — часть OwnerListingRepo: найти объявление продавца по внешнему id.
type leadTarget interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error)
}

// leadStore — часть LeadRepo.
type leadStore interface {
	Create(ctx context.Context, l domain.Lead) (domain.Lead, error)
}

// leadLister — часть LeadRepo для кабинета продавца.
type leadLister interface {
	ListForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]domain.Lead, int, error)
}

type LeadService struct {
	targets leadTarget
	leads   leadStore
	lists   leadLister
}

func NewLeadService(targets *repository.OwnerListingRepo, leads *repository.LeadRepo) *LeadService {
	return &LeadService{targets: targets, leads: leads, lists: leads}
}

// ListForSeller. sellerID берётся ИЗ СЕССИИ вызывающим хендлером и никогда из
// параметров запроса — иначе чужие заявки читались бы подстановкой id в URL.
func (s *LeadService) ListForSeller(ctx context.Context, sellerID uuid.UUID,
	limit, offset int) ([]domain.Lead, int, error) {
	return s.lists.ListForSeller(ctx, sellerID, limit, offset)
}

// ValidateLeadInput нормализует и проверяет поля заявки. Вынесена из Send
// намеренно: хендлер обязан вызвать её ДО того, как заведёт гостю аккаунт —
// иначе человек с пустым телефоном сначала регистрировался бы и только потом
// видел ошибку формы.
func ValidateLeadInput(in LeadInput) (LeadInput, error) {
	out := LeadInput{
		Name:    strings.TrimSpace(in.Name),
		Contact: strings.TrimSpace(in.Contact),
		Message: strings.TrimSpace(in.Message),
	}
	if out.Name == "" {
		return LeadInput{}, apperr.Validation("Представьтесь — продавцу нужно знать, кто пишет")
	}
	if out.Contact == "" {
		return LeadInput{}, apperr.Validation("Оставьте телефон или другой способ связи")
	}
	if len(out.Name) > leadNameMaxLen || len(out.Contact) > leadContactMaxLen ||
		len(out.Message) > leadMessageMaxLen {
		return LeadInput{}, apperr.Validation("Слишком длинный текст заявки")
	}
	return out, nil
}

// ResolveTarget проверяет, что заявке есть куда идти: объявление существует,
// опубликовано и не принадлежит самому отправителю. Вынесена из Send отдельным
// экспортированным методом, чтобы хендлер мог проверить пригодность цели ДО
// того, как заведёт гостю аккаунт, — иначе гость получал бы новый пароль ради
// заявки, которая тут же откажет 404. Send вызывает её же: сервис не должен
// полагаться на то, что вызывающий уже проверил цель сам.
func (s *LeadService) ResolveTarget(ctx context.Context, buyerID uuid.UUID,
	externalID string) (domain.OwnerListing, error) {
	listing, err := s.targets.GetByExternalID(ctx, externalID)
	if errors.Is(err, repository.ErrNotFound) {
		// Объект витринный — продавца в системе нет, заявке некуда идти.
		return domain.OwnerListing{}, apperr.LeadTargetNotFound()
	}
	if err != nil {
		return domain.OwnerListing{}, err
	}
	// Заявки принимает только опубликованное: черновик и снятое с витрины
	// продавец скрыл сознательно. Тот же критерий, что у contact.kind в паспорте.
	if listing.Status != "published" {
		return domain.OwnerListing{}, apperr.LeadTargetNotFound()
	}
	if listing.UserID == buyerID {
		return domain.OwnerListing{}, apperr.LeadToSelf()
	}
	return listing, nil
}

func (s *LeadService) Send(ctx context.Context, buyerID uuid.UUID, externalID string,
	in LeadInput) (domain.Lead, error) {
	// Повторная проверка, даже если хендлер уже вызывал ValidateLeadInput:
	// сервис не полагается на дисциплину вызывающего.
	in, err := ValidateLeadInput(in)
	if err != nil {
		return domain.Lead{}, err
	}
	name, contact, message := in.Name, in.Contact, in.Message

	listing, err := s.ResolveTarget(ctx, buyerID, externalID)
	if err != nil {
		return domain.Lead{}, err
	}

	listingID := listing.ID
	lead, err := s.leads.Create(ctx, domain.Lead{
		ListingID: &listingID, SellerID: listing.UserID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Address: listing.Address,
		Name: name, Contact: contact, Message: message,
	})
	if errors.Is(err, repository.ErrDuplicateLead) {
		return domain.Lead{}, apperr.LeadAlreadySent()
	}
	if err != nil {
		return domain.Lead{}, err
	}
	return lead, nil
}
