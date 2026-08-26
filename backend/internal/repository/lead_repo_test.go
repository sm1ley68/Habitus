package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

// newTestOwnerListing заводит ОПУБЛИКОВАННОЕ объявление продавца — цель заявки.
// Публикация делается отдельным SetStatus: Create не пишет колонку status
// (её нет в его INSERT), и объявление всегда рождается черновиком.
func newTestOwnerListing(t *testing.T, repo *OwnerListingRepo, sellerID uuid.UUID) domain.OwnerListing {
	t.Helper()
	ctx := context.Background()
	lng, lat := 37.6739, 55.7086
	l, err := repo.Create(ctx, domain.OwnerListing{
		UserID: sellerID, ExternalID: newExternalID(), Origin: "manual",
		City: "msk", Address: "Москва, Кожуховская улица, 14",
		Lng: &lng, Lat: &lat,
	})
	if err != nil {
		t.Fatalf("создать объявление: %v", err)
	}
	if err := repo.SetStatus(ctx, l.ID, "published", ""); err != nil {
		t.Fatalf("опубликовать объявление: %v", err)
	}
	published, err := repo.GetOwned(ctx, l.ID, sellerID)
	if err != nil {
		t.Fatalf("перечитать объявление: %v", err)
	}
	return published
}

func TestLeadCreateAndListForSeller(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	created, err := leads.Create(ctx, domain.Lead{
		ListingID: &listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Address: listing.Address,
		Name: "Иван", Contact: "+7 999 000-00-00",
		Message: "Можно посмотреть в субботу?",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.ID == uuid.Nil {
		t.Fatal("id заявки пустой")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("created_at не заполнен")
	}

	rows, total, err := leads.ListForSeller(ctx, sellerID, 10, 0)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d, ожидалось по 1", total, len(rows))
	}
	if rows[0].Contact != "+7 999 000-00-00" {
		t.Fatalf("contact = %q", rows[0].Contact)
	}
	if rows[0].Address != listing.Address {
		t.Fatalf("address = %q, ожидался %q", rows[0].Address, listing.Address)
	}
}

// Повтор гасится уникальным индексом, а не проверкой-перед-вставкой: две
// одновременные отправки иначе обе прошли бы.
func TestLeadCreateRejectsDuplicate(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)
	lead := domain.Lead{
		ListingID: &listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: "Иван", Contact: "+7 999 000-00-00",
	}

	if _, err := leads.Create(ctx, lead); err != nil {
		t.Fatalf("первая заявка: %v", err)
	}
	_, err := leads.Create(ctx, lead)

	if err != ErrDuplicateLead {
		t.Fatalf("err = %v, ожидался ErrDuplicateLead", err)
	}
}

// Чужие заявки в кабинет не попадают ни при каких обстоятельствах.
func TestLeadListForSellerIsScoped(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	otherSellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	if _, err := leads.Create(ctx, domain.Lead{
		ListingID: &listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Name: "Иван", Contact: "+7 999 000-00-00",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, total, err := leads.ListForSeller(ctx, otherSellerID, 10, 0)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if total != 0 || len(rows) != 0 {
		t.Fatalf("чужому продавцу видно %d заявок", len(rows))
	}
}

// Продавец снимает и удаляет объявление — заявка не должна пропасть вместе с
// ним: 0012 обещала это комментарием, но FK был CASCADE. 0016 чинит FK на
// SET NULL — эта проверка и есть регресс-тест на то обещание.
func TestLeadSurvivesListingDeletion(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	created, err := leads.Create(ctx, domain.Lead{
		ListingID: &listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Address: listing.Address,
		Name: "Иван", Contact: "+7 999 000-00-00",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := owners.Delete(ctx, listing.ID, sellerID); err != nil {
		t.Fatalf("удалить объявление: %v", err)
	}

	rows, total, err := leads.ListForSeller(ctx, sellerID, 10, 0)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, строк = %d; заявка должна остаться видимой", total, len(rows))
	}
	if rows[0].ID != created.ID {
		t.Fatalf("id = %s, ожидался %s", rows[0].ID, created.ID)
	}
	if rows[0].ListingID != nil {
		t.Fatalf("listing_id = %v, ожидался nil (объявление удалено)", *rows[0].ListingID)
	}
	if rows[0].Address != listing.Address {
		t.Fatalf("address = %q, ожидался сохранённый %q (объявления уже нет — брать неоткуда)",
			rows[0].Address, listing.Address)
	}
}

// «Показать ещё» дошёл до конца списка: пустая страница не должна занижать
// total. Оконная функция COUNT(*) OVER () именно так себя и вела — на офсете
// за пределами списка окно не возвращает ни одной строки.
func TestLeadListForSellerReportsTotalPastLastPage(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	owners := NewOwnerListingRepo(pool)
	leads := NewLeadRepo(pool)
	ctx := context.Background()

	sellerID := newTestUser(t, users)
	buyerID := newTestUser(t, users)
	listing := newTestOwnerListing(t, owners, sellerID)

	if _, err := leads.Create(ctx, domain.Lead{
		ListingID: &listing.ID, SellerID: sellerID, BuyerID: buyerID,
		ExternalID: listing.ExternalID, Address: listing.Address,
		Name: "Иван", Contact: "+7 999 000-00-00",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	rows, total, err := leads.ListForSeller(ctx, sellerID, 10, 50)
	if err != nil {
		t.Fatalf("ListForSeller: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("строк = %d, ожидалось 0 (офсет за пределами списка)", len(rows))
	}
	if total != 1 {
		t.Fatalf("total = %d, ожидался честный 1, а не занижен пустой страницей", total)
	}
}
