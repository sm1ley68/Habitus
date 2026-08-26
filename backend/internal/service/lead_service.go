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

type LeadService struct {
	targets leadTarget
	leads   leadStore
}

func NewLeadService(targets *repository.OwnerListingRepo, leads *repository.LeadRepo) *LeadService {
	return &LeadService{targets: targets, leads: leads}
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

func (s *LeadService) Send(ctx context.Context, buyerID uuid.UUID, externalID string,
	in LeadInput) (domain.Lead, error) {
	// Повторная проверка, даже если хендлер уже вызывал ValidateLeadInput:
	// сервис не полагается на дисциплину вызывающего.
	in, err := ValidateLeadInput(in)
	if err != nil {
		return domain.Lead{}, err
	}
	name, contact, message := in.Name, in.Contact, in.Message

	listing, err := s.targets.GetByExternalID(ctx, externalID)
	if errors.Is(err, repository.ErrNotFound) {
		// Объект витринный — продавца в системе нет, заявке некуда идти.
		return domain.Lead{}, apperr.LeadTargetNotFound()
	}
	if err != nil {
		return domain.Lead{}, err
	}
	// Заявки принимает только опубликованное: черновик и снятое с витрины
	// продавец скрыл сознательно. Тот же критерий, что у contact.kind в паспорте.
	if listing.Status != "published" {
		return domain.Lead{}, apperr.LeadTargetNotFound()
	}
	if listing.UserID == buyerID {
		return domain.Lead{}, apperr.LeadToSelf()
	}

	lead, err := s.leads.Create(ctx, domain.Lead{
		ListingID: listing.ID, SellerID: listing.UserID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: name, Contact: contact, Message: message,
	})
	if errors.Is(err, repository.ErrDuplicateLead) {
		return domain.Lead{}, apperr.LeadAlreadySent()
	}
	if err != nil {
		return domain.Lead{}, err
	}
	return lead, nil
}
