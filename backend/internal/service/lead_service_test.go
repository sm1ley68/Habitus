package service

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

type fakeLeadTarget struct {
	listing domain.OwnerListing
	err     error
}

func (f fakeLeadTarget) GetByExternalID(context.Context, string) (domain.OwnerListing, error) {
	return f.listing, f.err
}

type fakeLeadStore struct {
	created domain.Lead
	err     error
}

func (f *fakeLeadStore) Create(_ context.Context, l domain.Lead) (domain.Lead, error) {
	if f.err != nil {
		return domain.Lead{}, f.err
	}
	f.created = l
	l.ID = uuid.New()
	return l, nil
}

func newLeadService(target fakeLeadTarget, store *fakeLeadStore) *LeadService {
	return &LeadService{targets: target, leads: store}
}

func TestLeadSendFillsSellerFromListing(t *testing.T) {
	sellerID := uuid.New()
	listingID := uuid.New()
	store := &fakeLeadStore{}
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: listingID, UserID: sellerID, Status: "published", ExternalID: "cian_1",
	}}, store)
	buyerID := uuid.New()

	got, err := svc.Send(context.Background(), buyerID, "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00", Message: "В субботу?"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got.ID == uuid.Nil {
		t.Fatal("id заявки пустой")
	}
	if store.created.SellerID != sellerID {
		t.Fatalf("seller_id = %s, ожидался %s", store.created.SellerID, sellerID)
	}
	if store.created.ListingID != listingID {
		t.Fatalf("listing_id = %s, ожидался %s", store.created.ListingID, listingID)
	}
	if store.created.BuyerID != buyerID {
		t.Fatalf("buyer_id = %s, ожидался %s", store.created.BuyerID, buyerID)
	}
}

// Объект витринный (продавца в системе нет) — заявке некуда идти. 404 с
// собственным кодом, а не молчаливое «ок»: фронт обязан показать уход на
// источник, а не форму заявки.
func TestLeadSendRejectsListingWithoutSeller(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{err: repository.ErrNotFound}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_target_not_found")
}

// Неопубликованное объявление продавец скрыл сознательно — заявки не принимает.
func TestLeadSendRejectsUnpublishedListing(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "draft",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_target_not_found")
}

func TestLeadSendRejectsSelf(t *testing.T) {
	sellerID := uuid.New()
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: sellerID, Status: "published",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), sellerID, "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_to_self")
}

func TestLeadSendRequiresContact(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "published",
	}}, &fakeLeadStore{})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "   "})

	assertAppErrCode(t, err, "validation_error")
}

func TestLeadSendMapsDuplicateTo409(t *testing.T) {
	svc := newLeadService(fakeLeadTarget{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "published",
	}}, &fakeLeadStore{err: repository.ErrDuplicateLead})

	_, err := svc.Send(context.Background(), uuid.New(), "cian_1",
		LeadInput{Name: "Иван", Contact: "+7 999 000-00-00"})

	assertAppErrCode(t, err, "lead_already_sent")
}

func assertAppErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("err = nil, ожидался %s", code)
	}
	appErr, ok := err.(*apperr.Error)
	if !ok || appErr.Code != code {
		t.Fatalf("err = %#v, ожидался *apperr.Error{Code: %s}", err, code)
	}
}
