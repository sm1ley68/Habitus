package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/cian"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// --- заглушки зависимостей -------------------------------------------------

type fakeOwners struct {
	byExternal map[string]domain.OwnerListing
	created    []domain.OwnerListing
	createErr  error
}

func (f *fakeOwners) GetByExternalID(_ context.Context, externalID string) (domain.OwnerListing, error) {
	if l, ok := f.byExternal[externalID]; ok {
		return l, nil
	}
	return domain.OwnerListing{}, repository.ErrNotFound
}

func (f *fakeOwners) Create(_ context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	if f.createErr != nil {
		return domain.OwnerListing{}, f.createErr
	}
	l.ID = uuid.New()
	l.Status = "draft"
	l.Verification = "unverified"
	f.created = append(f.created, l)
	return l, nil
}

type fakeShowcase struct {
	snapshots map[string]domain.ListingSnapshot
	similar   []domain.SimilarListing
}

func (f *fakeShowcase) SnapshotByExternalID(_ context.Context, externalID string) (domain.ListingSnapshot, error) {
	if s, ok := f.snapshots[externalID]; ok {
		return s, nil
	}
	return domain.ListingSnapshot{}, repository.ErrNotFound
}

func (f *fakeShowcase) FindSimilar(_ context.Context, _, _ *float64, _, _ *int, _ *float32, _ string) ([]domain.SimilarListing, error) {
	return f.similar, nil
}

type fakeFetcher struct {
	listing cian.Listing
	err     error
	calls   int
}

func (f *fakeFetcher) FetchByID(_ context.Context, _ int64) (cian.Listing, error) {
	f.calls++
	return f.listing, f.err
}

func intp(v int) *int         { return &v }
func f32p(v float32) *float32 { return &v }
func i64p(v int64) *int64     { return &v }
func f64p(v float64) *float64 { return &v }

func newService(owners *fakeOwners, showcase *fakeShowcase, fetcher *fakeFetcher) *OwnerImportService {
	now := func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	return NewOwnerImportService(owners, showcase, fetcher,
		cian.NewRateLimiter(100, now), cian.NewUserQuota(100, now))
}

func sampleCianListing() cian.Listing {
	return cian.Listing{
		CianID: "318394906", Description: "Тихая двушка",
		Price: i64p(12_500_000), Area: f64p(54.3), Rooms: intp(2),
		Floor: intp(4), Floors: intp(17),
		Address:  "Москва, улица Мельникова, 3к1",
		Photos:   []string{"https://images.cdn-cian.ru/1.jpg"},
		Latitude: f64p(55.7108), Longitude: f64p(37.6595),
		URL: "https://www.cian.ru/sale/flat/318394906/",
	}
}

// --- тесты -----------------------------------------------------------------

func TestPreviewRejectsGarbageURL(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{})
	_, err := svc.Preview(context.Background(), uuid.New(), "моя квартира")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_url_invalid" {
		t.Fatalf("ожидался cian_url_invalid, получено %v", err)
	}
}

func TestPreviewAlreadyYours(t *testing.T) {
	userID := uuid.New()
	existing := domain.OwnerListing{ID: uuid.New(), UserID: userID, ExternalID: "cian_318394906"}
	fetcher := &fakeFetcher{}
	svc := newService(&fakeOwners{byExternal: map[string]domain.OwnerListing{
		"cian_318394906": existing,
	}}, &fakeShowcase{}, fetcher)

	preview, err := svc.Preview(context.Background(), userID, "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictAlreadyYours {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if preview.ExistingID == nil || *preview.ExistingID != existing.ID {
		t.Fatal("должен вернуться id уже существующей карточки")
	}
	if fetcher.calls != 0 {
		t.Fatal("своё объявление не требует похода в Циан")
	}
}

func TestPreviewClaimedByOther(t *testing.T) {
	fetcher := &fakeFetcher{}
	svc := newService(&fakeOwners{byExternal: map[string]domain.OwnerListing{
		"cian_318394906": {ID: uuid.New(), UserID: uuid.New()},
	}}, &fakeShowcase{}, fetcher)

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "listing_claimed_by_other" {
		t.Fatalf("ожидался listing_claimed_by_other, получено %v", err)
	}
	if fetcher.calls != 0 {
		t.Fatal("чужое объявление не требует похода в Циан")
	}
}

func TestPreviewClaimableSkipsCian(t *testing.T) {
	fetcher := &fakeFetcher{}
	showcase := &fakeShowcase{snapshots: map[string]domain.ListingSnapshot{
		"cian_318394906": {
			ExternalID: "cian_318394906", Source: "cian", City: "msk",
			Price: i64p(12_500_000), Area: f32p(54.3), Rooms: intp(2),
			Level: intp(4), Levels: intp(17),
			Address: "Москва, улица Мельникова, 3к1",
			Lng:     f64p(37.6595), Lat: f64p(55.7108),
			Photos: []string{"https://images.cdn-cian.ru/1.jpg"},
		},
	}}
	svc := newService(&fakeOwners{}, showcase, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictClaimable {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if fetcher.calls != 0 {
		t.Fatal("объявление уже в базе — идти в Циан незачем")
	}
	if preview.Draft.Address != "Москва, улица Мельникова, 3к1" || *preview.Draft.Rooms != 2 {
		t.Fatalf("черновик собран неверно: %+v", preview.Draft)
	}
}

func TestPreviewNewGoesToCian(t *testing.T) {
	fetcher := &fakeFetcher{listing: sampleCianListing()}
	svc := newService(&fakeOwners{}, &fakeShowcase{}, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictNew {
		t.Fatalf("вердикт = %q", preview.Verdict)
	}
	if fetcher.calls != 1 {
		t.Fatalf("ожидался один запрос в Циан, было %d", fetcher.calls)
	}
	if preview.Draft.ExternalID != "cian_318394906" || preview.Draft.Origin != "cian" {
		t.Fatalf("черновик собран неверно: %+v", preview.Draft)
	}
	if *preview.Draft.Level != 4 || *preview.Draft.Levels != 17 {
		t.Fatalf("этаж/этажность не перенесены: %+v", preview.Draft)
	}
}

func TestPreviewSurfacesSimilarAlongsideVerdict(t *testing.T) {
	fetcher := &fakeFetcher{listing: sampleCianListing()}
	showcase := &fakeShowcase{similar: []domain.SimilarListing{
		{ExternalID: "cian_777", Address: "Мельникова 3к1", Price: i64p(12_000_000)},
	}}
	svc := newService(&fakeOwners{}, showcase, fetcher)

	preview, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Verdict != VerdictNew {
		t.Fatalf("похожий объект не должен менять вердикт, получено %q", preview.Verdict)
	}
	if len(preview.Similar) != 1 {
		t.Fatalf("похожие не доехали: %+v", preview.Similar)
	}
}

func TestPreviewMapsCianBlockToUnavailable(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{err: cian.ErrBlocked})

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_unavailable" {
		t.Fatalf("ожидался cian_unavailable, получено %v", err)
	}
}

func TestPreviewMapsOfferNotFound(t *testing.T) {
	svc := newService(&fakeOwners{}, &fakeShowcase{}, &fakeFetcher{err: cian.ErrOfferNotFound})

	_, err := svc.Preview(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "cian_offer_not_found" {
		t.Fatalf("ожидался cian_offer_not_found, получено %v", err)
	}
}

func TestPreviewRespectsUserQuota(t *testing.T) {
	now := func() time.Time { return time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC) }
	svc := NewOwnerImportService(&fakeOwners{}, &fakeShowcase{},
		&fakeFetcher{listing: sampleCianListing()},
		cian.NewRateLimiter(100, now), cian.NewUserQuota(1, now))
	userID := uuid.New()
	url := "https://www.cian.ru/sale/flat/318394906/"

	if _, err := svc.Preview(context.Background(), userID, url); err != nil {
		t.Fatalf("первый импорт: %v", err)
	}
	_, err := svc.Preview(context.Background(), userID, url)

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "rate_limited" {
		t.Fatalf("ожидался rate_limited, получено %v", err)
	}
}

func TestImportCreatesOwnerListing(t *testing.T) {
	owners := &fakeOwners{}
	svc := newService(owners, &fakeShowcase{}, &fakeFetcher{listing: sampleCianListing()})
	userID := uuid.New()

	created, err := svc.Import(context.Background(), userID, "https://www.cian.ru/sale/flat/318394906/")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if created.ExternalID != "cian_318394906" || created.UserID != userID {
		t.Fatalf("создано не то: %+v", created)
	}
	if len(owners.created) != 1 {
		t.Fatalf("ожидалась одна вставка, было %d", len(owners.created))
	}
}

func TestImportRaceLosesToUniqueConstraint(t *testing.T) {
	owners := &fakeOwners{createErr: repository.ErrExternalIDTaken}
	svc := newService(owners, &fakeShowcase{}, &fakeFetcher{listing: sampleCianListing()})

	_, err := svc.Import(context.Background(), uuid.New(), "https://www.cian.ru/sale/flat/318394906/")

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "listing_claimed_by_other" {
		t.Fatalf("ожидался listing_claimed_by_other, получено %v", err)
	}
}
