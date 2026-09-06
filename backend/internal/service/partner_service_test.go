package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/repository"
)

// fakePartnerStore — минимум, нужный аутентификации: один ключ и один партнёр.
type fakePartnerStore struct {
	key     domain.APIKey
	partner domain.Partner
	found   bool
	touched int
	logged  []domain.AdminAction
	bySlug  *domain.Partner
}

func (f *fakePartnerStore) GetKeyWithPartner(_ context.Context, prefix string) (domain.APIKey, domain.Partner, error) {
	if !f.found || prefix != f.key.Prefix {
		return domain.APIKey{}, domain.Partner{}, repository.ErrNotFound
	}
	return f.key, f.partner, nil
}
func (f *fakePartnerStore) TouchKey(context.Context, uuid.UUID, time.Time) error {
	f.touched++
	return nil
}
func (f *fakePartnerStore) Create(context.Context, domain.Partner) (domain.Partner, error) {
	return f.partner, nil
}
func (f *fakePartnerStore) GetBySlug(context.Context, string) (domain.Partner, error) {
	if f.bySlug == nil {
		return domain.Partner{}, repository.ErrNotFound
	}
	return *f.bySlug, nil
}
func (f *fakePartnerStore) List(context.Context) ([]domain.Partner, error) { return nil, nil }
func (f *fakePartnerStore) SetStatus(_ context.Context, _ uuid.UUID, status string) error {
	if f.bySlug != nil {
		f.bySlug.Status = status
	}
	return nil
}
func (f *fakePartnerStore) CreateKey(_ context.Context, k domain.APIKey) (domain.APIKey, error) {
	k.ID = uuid.New()
	return k, nil
}
func (f *fakePartnerStore) ListKeys(context.Context, uuid.UUID) ([]domain.APIKey, error) {
	return nil, nil
}
func (f *fakePartnerStore) RevokeKey(context.Context, string) error { return nil }
func (f *fakePartnerStore) LogAdminAction(_ context.Context, a domain.AdminAction) error {
	f.logged = append(f.logged, a)
	return nil
}
func (f *fakePartnerStore) ListAdminLog(context.Context, string, int) ([]domain.AdminAction, error) {
	return f.logged, nil
}

// testPepper — тот же секрет, что подставляется в сервис под тестом. Ключ,
// посчитанный с одним перцем, не должен подходить к сервису с другим — это
// проверяет отдельный тест ниже.
const testPepper = "test-pepper"

func storeWith(t *testing.T, mutate func(*domain.APIKey, *domain.Partner)) (*fakePartnerStore, string) {
	t.Helper()
	prefix, full, hash, err := NewPartnerService(nil, nil, testPepper).newAPIKey("live")
	if err != nil {
		t.Fatalf("newAPIKey() error = %v", err)
	}
	store := &fakePartnerStore{
		found: true,
		key: domain.APIKey{ID: uuid.New(), Prefix: prefix, SecretHash: hash,
			Environment: "live", Scopes: []string{ScopeSearchRead}},
		partner: domain.Partner{ID: uuid.New(), Slug: "gorod", Status: domain.PartnerStatusActive},
	}
	if mutate != nil {
		mutate(&store.key, &store.partner)
	}
	return store, full
}

func TestNewAPIKeyCarriesEnvironmentAndSplitsBack(t *testing.T) {
	svc := NewPartnerService(nil, nil, testPepper)
	prefix, full, hash, err := svc.newAPIKey("test")
	if err != nil {
		t.Fatalf("newAPIKey() error = %v", err)
	}
	if !strings.HasPrefix(prefix, "hab_test_") {
		t.Fatalf("prefix = %q; среда должна читаться прямо из ключа", prefix)
	}
	gotPrefix, secret, ok := SplitAPIKey(full)
	if !ok || gotPrefix != prefix {
		t.Fatalf("SplitAPIKey(%q) = %q, %v; want %q, true", full, gotPrefix, ok, prefix)
	}
	if svc.hashSecret(secret) != hash {
		t.Fatal("хеш разобранного секрета не совпал с сохранённым")
	}
	if strings.Contains(full, hash) {
		t.Fatal("хеш не должен быть частью выданного ключа")
	}
}

func TestSplitAPIKeyRejectsForeignFormats(t *testing.T) {
	for _, raw := range []string{"", "hab_live_", "nothab_live_abc_def", "hab_live", "sk-1234"} {
		if _, _, ok := SplitAPIKey(raw); ok {
			t.Errorf("SplitAPIKey(%q) принял чужой формат", raw)
		}
	}
}

func TestAuthenticateAcceptsValidKey(t *testing.T) {
	store, full := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)

	identity, err := svc.Authenticate(context.Background(), full)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if identity.Partner.Slug != "gorod" {
		t.Fatalf("partner = %q; want gorod", identity.Partner.Slug)
	}
	if !identity.HasScope(ScopeSearchRead) || identity.HasScope(ScopeLeadsRead) {
		t.Fatal("скоупы ключа прочитаны неверно")
	}
}

func TestAuthenticateRejectsWrongSecretWithSamePrefix(t *testing.T) {
	store, full := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)

	prefix, _, _ := SplitAPIKey(full)
	_, err := svc.Authenticate(context.Background(), prefix+"_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	assertAppErr(t, err, 401, "invalid_api_key")
}

// Отказы по несуществующему префиксу и по неверному секрету обязаны быть
// неразличимы: иначе перебором выясняется, какие префиксы существуют.
func TestAuthenticateHidesWhetherPrefixExists(t *testing.T) {
	store, full := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)
	prefix, _, _ := SplitAPIKey(full)

	_, wrongSecret := svc.Authenticate(context.Background(), prefix+"_zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
	store.found = false
	_, noSuchKey := svc.Authenticate(context.Background(), full)

	if wrongSecret.Error() != noSuchKey.Error() {
		t.Fatalf("разные сообщения: %q против %q", wrongSecret, noSuchKey)
	}
}

func TestAuthenticateRejectsRevokedExpiredAndSuspended(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		mutate func(*domain.APIKey, *domain.Partner)
		code   string
		status int
	}{
		{"отозванный ключ", func(k *domain.APIKey, _ *domain.Partner) { k.RevokedAt = &past },
			"api_key_revoked", 401},
		{"истёкший ключ", func(k *domain.APIKey, _ *domain.Partner) { k.ExpiresAt = &past },
			"api_key_expired", 401},
		{"приостановленный партнёр", func(_ *domain.APIKey, p *domain.Partner) {
			p.Status = domain.PartnerStatusSuspended
		}, "partner_suspended", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, full := storeWith(t, tc.mutate)
			_, err := NewPartnerService(store, nil, testPepper).Authenticate(context.Background(), full)
			assertAppErr(t, err, tc.status, tc.code)
		})
	}
}

func TestNormalizeScopesDropsDuplicatesAndRejectsUnknown(t *testing.T) {
	got, err := NormalizeScopes([]string{ScopeSearchRead, " ", ScopeSearchRead, ScopeGeoRead})
	if err != nil {
		t.Fatalf("NormalizeScopes() error = %v", err)
	}
	if len(got) != 2 || got[0] != ScopeSearchRead || got[1] != ScopeGeoRead {
		t.Fatalf("scopes = %v; want [search:read geo:read]", got)
	}

	// Опечатка в скоупе должна падать при выпуске ключа, а не превращаться в
	// молчаливое «прав нет» уже в бою.
	if _, err := NormalizeScopes([]string{"search:reed"}); err == nil {
		t.Fatal("неизвестный scope принят")
	}
	if _, err := NormalizeScopes(nil); err == nil {
		t.Fatal("ключ без единого scope принят")
	}
}

func assertAppErr(t *testing.T, err error, status int, code string) {
	t.Helper()
	var ae *apperr.Error
	if !asAppErr(err, &ae) {
		t.Fatalf("err = %v; ожидался apperr.Error", err)
	}
	if ae.Status != status || ae.Code != code {
		t.Fatalf("err = %d/%s; want %d/%s", ae.Status, ae.Code, status, code)
	}
}

// Перец — единственное, что мешает человеку с доступом к базе вписать себе
// рабочий ключ: хеш, посчитанный без перца или с чужим, не подойдёт.
func TestKeyHashIsUselessWithoutThePepper(t *testing.T) {
	store, full := storeWith(t, nil)

	if _, err := NewPartnerService(store, nil, "другой-перец").
		Authenticate(context.Background(), full); err == nil {
		t.Fatal("ключ прошёл проверку на чужом перце")
	}
	if _, err := NewPartnerService(store, nil, "").
		Authenticate(context.Background(), full); err == nil {
		t.Fatal("ключ прошёл проверку без перца")
	}
	if _, err := NewPartnerService(store, nil, testPepper).
		Authenticate(context.Background(), full); err != nil {
		t.Fatalf("на своём перце ключ должен работать: %v", err)
	}
}

func TestIssueKeyRefusesPartnerWithoutApproval(t *testing.T) {
	store, _ := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)

	pending := domain.Partner{ID: uuid.New(), Slug: "gorod",
		Status: domain.PartnerStatusPending}
	_, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		Partner: pending, Environment: "live", Scopes: []string{ScopeSearchRead},
		Operator: "op", Reason: "r",
	})
	if err == nil {
		t.Fatal("ключ выдан партнёру, которого не одобрили")
	}
	assertAppErr(t, err, 400, "validation_error")
}

func TestIssueKeyExpiresByDefaultAndLogsIssuance(t *testing.T) {
	store, _ := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)
	active := domain.Partner{ID: uuid.New(), Slug: "gorod",
		Status: domain.PartnerStatusActive}

	key, secret, err := svc.IssueKey(context.Background(), IssueKeyInput{
		Partner: active, Environment: "live", Scopes: []string{ScopeSearchRead},
		Operator: "yarik@laptop", Reason: "договор №14",
	})
	if err != nil {
		t.Fatalf("IssueKey() error = %v", err)
	}
	// Бессрочность только по явному Forever: молчаливо бессрочный ключ — это
	// утечка, которая не истекает.
	if key.ExpiresAt == nil {
		t.Fatal("ключ без --forever обязан иметь срок")
	}
	if got := time.Until(*key.ExpiresAt); got < DefaultKeyTTL-time.Hour || got > DefaultKeyTTL+time.Hour {
		t.Fatalf("срок = %v; ожидался DefaultKeyTTL", got)
	}
	if secret == "" {
		t.Fatal("секрет не возвращён")
	}

	if len(store.logged) != 1 {
		t.Fatalf("записей журнала = %d; want 1", len(store.logged))
	}
	entry := store.logged[0]
	if entry.Action != domain.AdminActionKeyIssued || entry.Operator != "yarik@laptop" ||
		entry.Reason != "договор №14" || entry.KeyPrefix != key.Prefix {
		t.Fatalf("запись журнала = %+v", entry)
	}
	// Секрет в журнал не попадает ни при каких обстоятельствах.
	if strings.Contains(entry.KeyPrefix, secret) {
		t.Fatal("секрет утёк в журнал")
	}
}

func TestIssueKeyForeverIsExplicit(t *testing.T) {
	store, _ := storeWith(t, nil)
	svc := NewPartnerService(store, nil, testPepper)
	active := domain.Partner{ID: uuid.New(), Status: domain.PartnerStatusActive}

	key, _, err := svc.IssueKey(context.Background(), IssueKeyInput{
		Partner: active, Environment: "live", Scopes: []string{ScopeSearchRead},
		Forever: true, Operator: "op", Reason: "r",
	})
	if err != nil {
		t.Fatalf("IssueKey() error = %v", err)
	}
	if key.ExpiresAt != nil {
		t.Fatal("--forever должен давать ключ без срока")
	}
}

func TestNormalizeAllowedIPsAcceptsAddressesAndSubnets(t *testing.T) {
	got, err := NormalizeAllowedIPs([]string{"203.0.113.10", " 198.51.100.0/24 ", "", "2001:db8::1"})
	if err != nil {
		t.Fatalf("NormalizeAllowedIPs() error = %v", err)
	}
	want := []string{"203.0.113.10/32", "198.51.100.0/24", "2001:db8::1/128"}
	if len(got) != len(want) {
		t.Fatalf("got = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got = %v; want %v", got, want)
		}
	}
	// Кривая запись — отказ при выпуске: список, из которого тихо выпал
	// адрес, запирает партнёра снаружи, и разбираться он будет по 403 в бою.
	if _, err := NormalizeAllowedIPs([]string{"не адрес"}); err == nil {
		t.Fatal("мусор в списке адресов принят")
	}
}

func TestIPAllowed(t *testing.T) {
	// Пустой список — «откуда угодно»: это осознанный выбор при выпуске.
	if !IPAllowed(nil, "203.0.113.10") {
		t.Fatal("пустой список должен пропускать любой адрес")
	}
	allowed := []string{"203.0.113.10/32", "198.51.100.0/24"}
	for _, ip := range []string{"203.0.113.10", "198.51.100.7"} {
		if !IPAllowed(allowed, ip) {
			t.Errorf("IPAllowed(%s) = false; адрес в списке", ip)
		}
	}
	for _, ip := range []string{"203.0.113.11", "198.51.101.7", "", "не адрес"} {
		if IPAllowed(allowed, ip) {
			t.Errorf("IPAllowed(%q) = true; адреса в списке нет", ip)
		}
	}
}

func TestApproveOpensAccessAndIsRecorded(t *testing.T) {
	store, _ := storeWith(t, func(_ *domain.APIKey, p *domain.Partner) {
		p.Status = domain.PartnerStatusPending
	})
	store.bySlug = &store.partner
	svc := NewPartnerService(store, nil, testPepper)

	partner, err := svc.Approve(context.Background(), "gorod", "yarik", "договор №14")
	if err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if partner.Status != domain.PartnerStatusActive {
		t.Fatalf("status = %s; want active", partner.Status)
	}
	if len(store.logged) != 1 || store.logged[0].Action != domain.AdminActionPartnerApproved {
		t.Fatalf("журнал = %+v", store.logged)
	}
}
