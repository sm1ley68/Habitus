package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

// seedPartner заводит служебный аккаунт и партнёра под ним — ровно так же,
// как это делает PartnerService.Provision.
func seedPartner(t *testing.T, repo *PartnerRepo, users *UserRepo, slug string) domain.Partner {
	t.Helper()
	ctx := context.Background()
	user, err := users.Create(ctx, "partner+"+slug+"@habitus.invalid", "!partner", slug)
	if err != nil {
		t.Fatalf("создание служебного аккаунта: %v", err)
	}
	partner, err := repo.Create(ctx, domain.Partner{Slug: slug, Name: slug, UserID: user.ID})
	if err != nil {
		t.Fatalf("создание партнёра: %v", err)
	}
	return partner
}

// partnerRepos отдаёт чистый партнёрский контур. Чистка через users CASCADE:
// от служебного аккаунта каскадом уходят и партнёр, и всё, что под ним.
func partnerRepos(t *testing.T) (*PartnerRepo, *UserRepo) {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE partners, users CASCADE;`); err != nil {
		t.Fatalf("очистка партнёрских таблиц: %v", err)
	}
	return NewPartnerRepo(pool), NewUserRepo(pool)
}

func TestPartnerRepoFindsKeyWithPartnerInOneQuery(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	created, err := repo.CreateKey(ctx, domain.APIKey{
		PartnerID: partner.ID, Name: "CRM", Prefix: "hab_live_abcd1234",
		SecretHash: "deadbeef", Environment: "live",
		Scopes: []string{"search:read", "leads:read"},
	})
	if err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}

	key, gotPartner, err := repo.GetKeyWithPartner(ctx, "hab_live_abcd1234")
	if err != nil {
		t.Fatalf("GetKeyWithPartner() error = %v", err)
	}
	if key.ID != created.ID || gotPartner.ID != partner.ID {
		t.Fatalf("вернулись чужие ключ/партнёр: %v / %v", key.ID, gotPartner.ID)
	}
	if len(key.Scopes) != 2 || key.Scopes[0] != "search:read" {
		t.Fatalf("scopes = %v; want [search:read leads:read]", key.Scopes)
	}
	// Индивидуальные лимиты не заданы — это NULL, а не ноль: ноль означал бы
	// «ничего нельзя», а имелось в виду «взять общий из конфига».
	if gotPartner.RateLimitPerMin != nil || gotPartner.LLMPerHour != nil {
		t.Fatalf("лимиты = %v/%v; ожидались nil", gotPartner.RateLimitPerMin, gotPartner.LLMPerHour)
	}
}

func TestPartnerRepoRevokeIsNotDeletion(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")
	if _, err := repo.CreateKey(ctx, domain.APIKey{
		PartnerID: partner.ID, Prefix: "hab_live_abcd1234", SecretHash: "x",
		Environment: "live", Scopes: []string{"search:read"},
	}); err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}

	if err := repo.RevokeKey(ctx, "hab_live_abcd1234"); err != nil {
		t.Fatalf("RevokeKey() error = %v", err)
	}
	key, _, err := repo.GetKeyWithPartner(ctx, "hab_live_abcd1234")
	if err != nil {
		t.Fatalf("отозванный ключ должен оставаться читаемым: %v", err)
	}
	if key.RevokedAt == nil {
		t.Fatal("revoked_at не проставлен")
	}
	// Повторный отзыв уже отозванного — не молчаливый успех: иначе «отозвал»
	// и «такого ключа нет» неразличимы для администратора.
	if err := repo.RevokeKey(ctx, "hab_live_abcd1234"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("повторный отзыв = %v; ожидался ErrNotFound", err)
	}
}

func TestPartnerRepoSlugIsUnique(t *testing.T) {
	repo, users := partnerRepos(t)
	seedPartner(t, repo, users, "gorod")

	user, err := users.Create(context.Background(), "second@habitus.invalid", "!partner", "x")
	if err != nil {
		t.Fatalf("создание второго аккаунта: %v", err)
	}
	_, err = repo.Create(context.Background(), domain.Partner{
		Slug: "gorod", Name: "Дубль", UserID: user.ID})
	if !errors.Is(err, ErrSlugTaken) {
		t.Fatalf("Create() = %v; ожидался ErrSlugTaken", err)
	}
}

func TestIdempotencyReplaysSameBodyAndRejectsDifferent(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	rec := domain.IdempotencyRecord{
		PartnerID: partner.ID, Key: "k-1", Endpoint: "POST /partner/v1/search",
		RequestHash: "hash-a", StatusCode: 201, Response: []byte(`{"id":"s-1"}`),
	}
	if err := repo.SaveIdempotent(ctx, rec); err != nil {
		t.Fatalf("SaveIdempotent() error = %v", err)
	}

	got, err := repo.GetIdempotent(ctx, partner.ID, "k-1", "hash-a")
	if err != nil {
		t.Fatalf("GetIdempotent() error = %v", err)
	}
	if got.StatusCode != 201 || string(got.Response) != `{"id": "s-1"}` && string(got.Response) != `{"id":"s-1"}` {
		t.Fatalf("сохранённый ответ = %d %s", got.StatusCode, got.Response)
	}
	if _, err := repo.GetIdempotent(ctx, partner.ID, "k-1", "hash-b"); !errors.Is(err, ErrIdempotencyMismatch) {
		t.Fatalf("тот же ключ с другим телом = %v; ожидался ErrIdempotencyMismatch", err)
	}

	// Первый успевший ответ — канонический: перезапись сделала бы обещание
	// «тот же ключ — тот же ответ» неправдой.
	rec.Response = []byte(`{"id":"s-2"}`)
	if err := repo.SaveIdempotent(ctx, rec); err != nil {
		t.Fatalf("повторное сохранение: %v", err)
	}
	got, _ = repo.GetIdempotent(ctx, partner.ID, "k-1", "hash-a")
	if string(got.Response) == `{"id":"s-2"}` {
		t.Fatal("сохранённый ответ перезаписан вторым вызовом")
	}
}

func TestSweepIdempotencyDropsOnlyStaleKeys(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	for _, key := range []string{"fresh", "stale"} {
		if err := repo.SaveIdempotent(ctx, domain.IdempotencyRecord{
			PartnerID: partner.ID, Key: key, Endpoint: "POST /x",
			RequestHash: "h", StatusCode: 200, Response: []byte(`{}`),
		}); err != nil {
			t.Fatalf("SaveIdempotent(%s): %v", key, err)
		}
	}
	pool := testPool(t)
	if _, err := pool.Exec(ctx, `
		UPDATE partner_idempotency SET created_at = now() - interval '48 hours'
		WHERE key = 'stale'`); err != nil {
		t.Fatalf("состаривание записи: %v", err)
	}

	n, err := repo.SweepIdempotency(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("SweepIdempotency() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("удалено %d записей; want 1", n)
	}
	if _, err := repo.GetIdempotent(ctx, partner.ID, "fresh", "h"); err != nil {
		t.Fatalf("свежая запись не должна была пропасть: %v", err)
	}
}

func TestPartnerSearchStoresAndPagesResults(t *testing.T) {
	repo, users := partnerRepos(t)
	pool := testPool(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")
	searches := NewPartnerSearchRepo(pool)

	id, err := searches.Insert(ctx, domain.PartnerSearch{
		PartnerID: partner.ID, Query: "двушка у парка", City: "msk",
		ParsedQuery: map[string]any{"rooms": []any{float64(2)}},
		Relaxed:     []string{"радиус расширен"}, Total: 3,
	})
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	rows := []domain.PartnerSearchResult{
		{ExternalID: "a", Rank: 0, Score: 0.9, MatchScore: 91},
		{ExternalID: "b", Rank: 1, Score: 0.8, MatchScore: 84},
		{ExternalID: "c", Rank: 2, Score: 0.7, MatchScore: 77},
	}
	if err := searches.InsertResults(ctx, id, rows); err != nil {
		t.Fatalf("InsertResults() error = %v", err)
	}

	page, total, err := searches.ListResults(ctx, id, 2, 1)
	if err != nil {
		t.Fatalf("ListResults() error = %v", err)
	}
	// total считается отдельным COUNT(*): на странице за концом списка окно
	// над той же выборкой схлопнуло бы его в ноль.
	if total != 3 {
		t.Fatalf("total = %d; want 3", total)
	}
	if len(page) != 2 || page[0].ExternalID != "b" || page[1].ExternalID != "c" {
		t.Fatalf("страница = %+v; порядок ранга нарушен", page)
	}

	got, err := searches.GetOwned(ctx, partner.ID, id)
	if err != nil {
		t.Fatalf("GetOwned() error = %v", err)
	}
	if got.Query != "двушка у парка" || len(got.Relaxed) != 1 {
		t.Fatalf("поиск прочитан неверно: %+v", got)
	}
	// area_geojson остаётся NULL, когда зоны не было: пустой объект читался
	// бы как «зона посчитана и пуста».
	if got.AreaGeoJSON != nil {
		t.Fatalf("area_geojson = %v; ожидался null", got.AreaGeoJSON)
	}

	// Чужой партнёр не должен видеть поиск даже по точному id.
	if _, err := searches.GetOwned(ctx, uuid.New(), id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetOwned() чужим партнёром = %v; ожидался ErrNotFound", err)
	}
}

func TestPartnerSearchCachesDossierPerObject(t *testing.T) {
	repo, users := partnerRepos(t)
	pool := testPool(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")
	searches := NewPartnerSearchRepo(pool)

	id, err := searches.Insert(ctx, domain.PartnerSearch{PartnerID: partner.ID, City: "msk"})
	if err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := searches.InsertResults(ctx, id, []domain.PartnerSearchResult{{ExternalID: "a"}}); err != nil {
		t.Fatalf("InsertResults() error = %v", err)
	}

	before, _ := searches.GetResult(ctx, id, "a")
	if before.DossierUpdatedAt != nil {
		t.Fatal("до расчёта досье отметки времени быть не должно")
	}

	if err := searches.SaveDossier(ctx, id, "a", "dossier-v1",
		map[string]any{"verdict": map[string]any{"headline": "подходит"}}); err != nil {
		t.Fatalf("SaveDossier() error = %v", err)
	}
	after, err := searches.GetResult(ctx, id, "a")
	if err != nil {
		t.Fatalf("GetResult() error = %v", err)
	}
	if after.DossierVersion != "dossier-v1" || after.DossierUpdatedAt == nil {
		t.Fatalf("кэш досье не сохранился: %+v", after)
	}
	if after.Dossier["verdict"] == nil {
		t.Fatalf("тело досье потерялось: %v", after.Dossier)
	}
}

func TestWebhookQueueClaimsEachDeliveryOnce(t *testing.T) {
	repo, users := partnerRepos(t)
	pool := testPool(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")
	hooks := NewPartnerWebhookRepo(pool)

	hook, err := hooks.Create(ctx, domain.PartnerWebhook{
		PartnerID: partner.ID, URL: "https://crm.example.com/hook",
		Secret: "whsec_x", Events: []string{"lead.created"}, Active: true,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	subs, err := hooks.Subscribers(ctx, partner.ID, "lead.created")
	if err != nil || len(subs) != 1 {
		t.Fatalf("Subscribers() = %v, %v; want одна подписка", subs, err)
	}
	if other, _ := hooks.Subscribers(ctx, partner.ID, "listing.published"); len(other) != 0 {
		t.Fatal("подписка сработала на событие, на которое не подписывались")
	}

	if _, err := hooks.Enqueue(ctx, hook.ID, "lead.created",
		map[string]any{"id": "lead-1"}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	first, err := hooks.ClaimDue(ctx, 10, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("ClaimDue() = %v, %v; want одна доставка", first, err)
	}
	if first[0].URL != "https://crm.example.com/hook" || first[0].Secret != "whsec_x" {
		t.Fatalf("адрес и секрет должны приходить джойном: %+v", first[0])
	}
	// Взятая в работу доставка отложена на срок аренды — вторая реплика
	// шлюза не должна отправить тот же вебхук ещё раз.
	second, err := hooks.ClaimDue(ctx, 10, time.Minute)
	if err != nil {
		t.Fatalf("ClaimDue() error = %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("повторный захват вернул %d доставок; ожидалось 0", len(second))
	}
}

func TestWebhookDeliveryGivesUpAfterMaxAttempts(t *testing.T) {
	repo, users := partnerRepos(t)
	pool := testPool(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")
	hooks := NewPartnerWebhookRepo(pool)

	hook, _ := hooks.Create(ctx, domain.PartnerWebhook{
		PartnerID: partner.ID, URL: "https://crm.example.com/hook",
		Secret: "whsec_x", Events: []string{"lead.created"}, Active: true,
	})
	id, _ := hooks.Enqueue(ctx, hook.ID, "lead.created", map[string]any{})

	for i := 0; i < 2; i++ {
		if err := hooks.MarkFailed(ctx, id, "HTTP 500", time.Second, 3); err != nil {
			t.Fatalf("MarkFailed() error = %v", err)
		}
	}
	rows, _, err := hooks.ListDeliveries(ctx, hook.ID, 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListDeliveries() = %v, %v", rows, err)
	}
	if rows[0].Status != "pending" || rows[0].Attempts != 2 {
		t.Fatalf("после двух попыток: %s / %d", rows[0].Status, rows[0].Attempts)
	}
	// Причина отказа сохраняется: партнёр должен видеть, почему событие до
	// него не дошло, а не только что оно «не дошло».
	if rows[0].LastError != "HTTP 500" {
		t.Fatalf("last_error = %q", rows[0].LastError)
	}

	if err := hooks.MarkFailed(ctx, id, "HTTP 500", time.Second, 3); err != nil {
		t.Fatalf("MarkFailed() error = %v", err)
	}
	rows, _, _ = hooks.ListDeliveries(ctx, hook.ID, 10, 0)
	if rows[0].Status != "failed" {
		t.Fatalf("status = %q; после исчерпания попыток доставка должна закрыться", rows[0].Status)
	}
}

func TestKeyRemembersAllowedAddresses(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	if _, err := repo.CreateKey(ctx, domain.APIKey{
		PartnerID: partner.ID, Prefix: "hab_live_abcd1234", SecretHash: "x",
		Environment: "live", Scopes: []string{"search:read"},
		AllowedIPs: []string{"203.0.113.10/32", "198.51.100.0/24"},
	}); err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	key, _, err := repo.GetKeyWithPartner(ctx, "hab_live_abcd1234")
	if err != nil {
		t.Fatalf("GetKeyWithPartner() error = %v", err)
	}
	if len(key.AllowedIPs) != 2 || key.AllowedIPs[0] != "203.0.113.10/32" {
		t.Fatalf("allowed_ips = %v", key.AllowedIPs)
	}
}

func TestKeyWithoutAllowedAddressesIsEmptyNotNull(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	if _, err := repo.CreateKey(ctx, domain.APIKey{
		PartnerID: partner.ID, Prefix: "hab_live_abcd1234", SecretHash: "x",
		Environment: "live", Scopes: []string{"search:read"},
	}); err != nil {
		t.Fatalf("CreateKey() error = %v", err)
	}
	key, _, err := repo.GetKeyWithPartner(ctx, "hab_live_abcd1234")
	if err != nil {
		t.Fatalf("GetKeyWithPartner() error = %v", err)
	}
	// Пустой массив, а не NULL: колонка объявлена NOT NULL, и nil-срез из Go
	// уехал бы в неё как NULL.
	if key.AllowedIPs == nil || len(key.AllowedIPs) != 0 {
		t.Fatalf("allowed_ips = %#v; ожидался пустой массив", key.AllowedIPs)
	}
}

func TestPartnerStartsPendingUntilApproved(t *testing.T) {
	repo, users := partnerRepos(t)
	ctx := context.Background()

	user, err := users.Create(ctx, "partner+p@habitus.invalid", "!partner", "p")
	if err != nil {
		t.Fatalf("создание аккаунта: %v", err)
	}
	partner, err := repo.Create(ctx, domain.Partner{
		Slug: "pending-one", Name: "P", UserID: user.ID,
		Status: domain.PartnerStatusPending,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if partner.Status != domain.PartnerStatusPending {
		t.Fatalf("status = %s; want pending", partner.Status)
	}
	if err := repo.SetStatus(ctx, partner.ID, domain.PartnerStatusActive); err != nil {
		t.Fatalf("SetStatus() error = %v", err)
	}
	got, err := repo.GetBySlug(ctx, "pending-one")
	if err != nil {
		t.Fatalf("GetBySlug() error = %v", err)
	}
	if got.Status != domain.PartnerStatusActive {
		t.Fatalf("status = %s; want active", got.Status)
	}
}

func TestAdminLogSurvivesPartnerDeletion(t *testing.T) {
	repo, users := partnerRepos(t)
	pool := testPool(t)
	ctx := context.Background()
	partner := seedPartner(t, repo, users, "gorod")

	id := partner.ID
	if err := repo.LogAdminAction(ctx, domain.AdminAction{
		PartnerID: &id, Slug: partner.Slug, Action: domain.AdminActionKeyIssued,
		KeyPrefix: "hab_live_abcd1234", Operator: "yarik@laptop",
		Reason: "договор №14", Details: map[string]any{"scopes": []string{"search:read"}},
	}); err != nil {
		t.Fatalf("LogAdminAction() error = %v", err)
	}

	entries, err := repo.ListAdminLog(ctx, "gorod", 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListAdminLog() = %v, %v", entries, err)
	}
	if entries[0].Operator != "yarik@laptop" || entries[0].Reason != "договор №14" {
		t.Fatalf("запись = %+v", entries[0])
	}

	// Удалённый партнёр не должен уносить с собой запись о том, что ему
	// когда-то выдали доступ.
	if _, err := pool.Exec(ctx, `DELETE FROM partners WHERE id = $1`, partner.ID); err != nil {
		t.Fatalf("удаление партнёра: %v", err)
	}
	entries, err = repo.ListAdminLog(ctx, "gorod", 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("после удаления партнёра журнал = %v, %v", entries, err)
	}
	if entries[0].PartnerID != nil {
		t.Fatalf("partner_id = %v; ожидался NULL после удаления", entries[0].PartnerID)
	}
	if entries[0].Slug != "gorod" {
		t.Fatalf("slug = %q; он хранится копией именно ради этого случая", entries[0].Slug)
	}
}
