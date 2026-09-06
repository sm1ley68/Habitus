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
	return domain.Partner{}, repository.ErrNotFound
}
func (f *fakePartnerStore) List(context.Context) ([]domain.Partner, error)     { return nil, nil }
func (f *fakePartnerStore) SetStatus(context.Context, uuid.UUID, string) error { return nil }
func (f *fakePartnerStore) CreateKey(_ context.Context, k domain.APIKey) (domain.APIKey, error) {
	k.ID = uuid.New()
	return k, nil
}
func (f *fakePartnerStore) ListKeys(context.Context, uuid.UUID) ([]domain.APIKey, error) {
	return nil, nil
}
func (f *fakePartnerStore) RevokeKey(context.Context, string) error { return nil }

func storeWith(t *testing.T, mutate func(*domain.APIKey, *domain.Partner)) (*fakePartnerStore, string) {
	t.Helper()
	prefix, full, hash, err := NewAPIKey("live")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
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
	prefix, full, hash, err := NewAPIKey("test")
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	if !strings.HasPrefix(prefix, "hab_test_") {
		t.Fatalf("prefix = %q; среда должна читаться прямо из ключа", prefix)
	}
	gotPrefix, secret, ok := SplitAPIKey(full)
	if !ok || gotPrefix != prefix {
		t.Fatalf("SplitAPIKey(%q) = %q, %v; want %q, true", full, gotPrefix, ok, prefix)
	}
	if hashSecret(secret) != hash {
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
	svc := NewPartnerService(store, nil)

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
	svc := NewPartnerService(store, nil)

	prefix, _, _ := SplitAPIKey(full)
	_, err := svc.Authenticate(context.Background(), prefix+"_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

	assertAppErr(t, err, 401, "invalid_api_key")
}

// Отказы по несуществующему префиксу и по неверному секрету обязаны быть
// неразличимы: иначе перебором выясняется, какие префиксы существуют.
func TestAuthenticateHidesWhetherPrefixExists(t *testing.T) {
	store, full := storeWith(t, nil)
	svc := NewPartnerService(store, nil)
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
			_, err := NewPartnerService(store, nil).Authenticate(context.Background(), full)
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
