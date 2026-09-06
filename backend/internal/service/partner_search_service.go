// partner_search_service.go — поиск, досье и вопрос по объекту для Partner API.
//
// B2C держит контекст запроса в чате: разбор запроса лежит в chat_searches,
// выдача — в chat_search_results, и досье строится вокруг них. У интеграции
// чата нет, но контекст нужен ровно тот же: без разобранного запроса «этот
// объект подходит на 84%» не на чем основать. Поэтому поиск здесь —
// самостоятельный ресурс с собственным id, а всё остальное (пересчёт скора,
// сборка карточки, кэш досье) переиспользует те же функции, что и B2C.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/repository"
)

// partnerSearchStore — часть PartnerSearchRepo, нужная сервису. Обособленный
// интерфейс — тот же приём, что у chatSearchStore: прогнать сценарий без базы.
type partnerSearchStore interface {
	Insert(ctx context.Context, s domain.PartnerSearch) (uuid.UUID, error)
	GetOwned(ctx context.Context, partnerID, id uuid.UUID) (domain.PartnerSearch, error)
	InsertResults(ctx context.Context, searchID uuid.UUID, rows []domain.PartnerSearchResult) error
	ListResults(ctx context.Context, searchID uuid.UUID, limit, offset int) ([]domain.PartnerSearchResult, int, error)
	GetResult(ctx context.Context, searchID uuid.UUID, externalID string) (domain.PartnerSearchResult, error)
	SaveDossier(ctx context.Context, searchID uuid.UUID, externalID, version string, dossier map[string]any) error
}

// partnerListingSource — часть ListingRepo: статика объекта и время его
// последнего обновления (нужно, чтобы кэш досье не пережил сам объект).
type partnerListingSource interface {
	GetByExternalID(ctx context.Context, externalID string) (domain.Listing, error)
	GetByExternalIDs(ctx context.Context, ids []string) (map[string]domain.Listing, error)
	GetUpdatedAt(ctx context.Context, externalID string) (*time.Time, error)
}

type PartnerSearchService struct {
	searches   partnerSearchStore
	listings   partnerListingSource
	owners     ownerLookup
	ml         *client.MLClient
	searchTTO  time.Duration
	dossierTTO time.Duration
	askTTO     time.Duration
	ttlHours   int
}

func NewPartnerSearchService(searches partnerSearchStore, listings partnerListingSource,
	owners ownerLookup, ml *client.MLClient,
	searchTimeout, dossierTimeout, askTimeout time.Duration, dossierTTLHours int) *PartnerSearchService {
	return &PartnerSearchService{
		searches: searches, listings: listings, owners: owners, ml: ml,
		searchTTO: searchTimeout, dossierTTO: dossierTimeout, askTTO: askTimeout,
		ttlHours: dossierTTLHours,
	}
}

// PartnerSearchInput — то, что партнёр присылает в POST /partner/v1/search.
type PartnerSearchInput struct {
	Query string
	City  string
	Point *client.PointConstraint
	// Explain=true просит текстовое объяснение выдачи. Стоит отдельного вызова
	// LLM и заметных секунд, поэтому по умолчанию выключено: интеграции чаще
	// нужны объекты, а не проза о них.
	Explain bool
	Limit   int
	// PrevSearchID — уточнение предыдущего запроса тем же партнёром: разбор
	// прошлого шага уезжает в ML как prev_parsed, и «а теперь подешевле» не
	// теряет всё, что было сказано раньше.
	PrevSearchID *uuid.UUID
}

// PartnerSearchOutput — что уходит наружу: сам поиск и первая страница выдачи.
type PartnerSearchOutput struct {
	Search  domain.PartnerSearch
	Objects []FinalResultObject
	HasMore bool
}

const (
	partnerSearchDefaultLimit = 20
	partnerSearchMaxLimit     = 50
)

// Search выполняет синхронный поиск. В отличие от B2C здесь нет SSE: у
// интеграции нет пользователя, которому нужно показывать стадии, — ей нужен
// один ответ, и лучше честно долгий, чем поток событий, который придётся
// собирать обратно.
func (s *PartnerSearchService) Search(ctx context.Context, partner domain.Partner,
	in PartnerSearchInput) (PartnerSearchOutput, error) {
	if in.City == "" {
		in.City = "msk"
	}
	if err := validateCity(in.City); err != nil {
		return PartnerSearchOutput{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = partnerSearchDefaultLimit
	}
	if limit > partnerSearchMaxLimit {
		limit = partnerSearchMaxLimit
	}

	req := client.SearchRequest{
		Query: in.Query, City: in.City, Point: in.Point, Explain: in.Explain,
	}
	if in.PrevSearchID != nil {
		prev, err := s.searches.GetOwned(ctx, partner.ID, *in.PrevSearchID)
		if err != nil && !errors.Is(err, repository.ErrNotFound) {
			return PartnerSearchOutput{}, err
		}
		if err == nil {
			req.PrevParsed = prev.ParsedQuery
		}
	}

	mlCtx, cancel := context.WithTimeout(ctx, s.searchTTO)
	defer cancel()
	callStart := time.Now()
	resp, err := s.ml.Search(mlCtx, req)
	observability.Default.ObserveMLCall("partner_search", time.Since(callStart).Seconds())
	if err != nil {
		return PartnerSearchOutput{}, mlAppError(err, s.searchTTO)
	}
	for stage, ms := range resp.Timings {
		observability.Default.ObserveMLStage(stage, ms/1000)
	}
	for _, layer := range resp.Degraded {
		observability.Default.IncMLDegraded(layer)
	}

	ids := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		ids = append(ids, item.ExternalID)
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}

	objects := make([]FinalResultObject, 0, len(resp.Results))
	rows := make([]domain.PartnerSearchResult, 0, len(resp.Results))
	for rank, item := range resp.Results {
		obj, ok := BuildFinalResultObject(item, rank, resp.Degraded, listings)
		if !ok {
			// Объект пропал из витрины между ответом ML и этим запросом —
			// тот же отсев, что в B2C: карточку не из чего собрать.
			continue
		}
		objects = append(objects, obj)
		rows = append(rows, domain.PartnerSearchResult{
			ExternalID: obj.ID, Rank: rank, Price: item.Price, Area: item.Area,
			Rooms: item.Rooms, AddressFacts: item.AddressFacts, Score: item.Score,
			MatchScore: obj.MatchScore,
		})
	}

	search := domain.PartnerSearch{
		PartnerID: partner.ID, Query: in.Query, City: in.City,
		ParsedQuery: parsedQueryMap(resp.Parsed), Relaxed: resp.Relaxed,
		Degraded: resp.Degraded, Notes: resp.Notes,
		DataFreshness: resp.DataFreshness, AreaLabel: resp.AreaLabel,
		AreaGeoJSON: resp.AreaGeojson, Explanation: resp.Explanation,
		Total: len(objects),
	}
	id, err := s.searches.Insert(ctx, search)
	if err != nil {
		return PartnerSearchOutput{}, err
	}
	search.ID = id
	search.CreatedAt = time.Now().UTC()
	if err := s.searches.InsertResults(ctx, id, rows); err != nil {
		return PartnerSearchOutput{}, err
	}

	page := objects
	if len(page) > limit {
		page = page[:limit]
	}
	return PartnerSearchOutput{
		Search: search, Objects: page, HasMore: len(objects) > len(page),
	}, nil
}

// GetSearch — метаданные поиска: разбор запроса, ослабления, деградации.
func (s *PartnerSearchService) GetSearch(ctx context.Context, partnerID,
	searchID uuid.UUID) (domain.PartnerSearch, error) {
	search, err := s.searches.GetOwned(ctx, partnerID, searchID)
	if errors.Is(err, repository.ErrNotFound) {
		return domain.PartnerSearch{}, apperr.SearchNotFound()
	}
	return search, err
}

// Results — страница сохранённой выдачи. Повторный поиск для «показать ещё»
// не делается: весь пул уже лежит в базе, и ML второй раз не тревожат.
// Возвращает объекты, общее число сохранённых и число ПРОЧИТАННЫХ строк:
// последнее нужно курсору. Объект, пропавший из витрины после подбора, в
// выдачу не попадает, но страницу он занял — сдвинь курсор на показанное, и
// следующая страница повторит уже отданное.
func (s *PartnerSearchService) Results(ctx context.Context, partnerID, searchID uuid.UUID,
	limit, offset int) (objects []FinalResultObject, total, consumed int, err error) {
	if _, err := s.GetSearch(ctx, partnerID, searchID); err != nil {
		return nil, 0, 0, err
	}
	rows, total, err := s.searches.ListResults(ctx, searchID, limit, offset)
	if err != nil {
		return nil, 0, 0, err
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.ExternalID
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}
	out := make([]FinalResultObject, 0, len(rows))
	for _, r := range rows {
		obj, ok := BuildStoredResultObject(domain.ChatSearchResult{
			ExternalID: r.ExternalID, Price: r.Price, Area: r.Area, Rooms: r.Rooms,
			AddressFacts: r.AddressFacts, Score: r.Score, MatchScore: r.MatchScore,
		}, listings)
		if !ok {
			continue
		}
		out = append(out, obj)
	}
	return out, total, len(rows), nil
}

// Object — карточка объекта вне контекста поиска: только факты витрины.
// Ни процента совпадения, ни досье здесь быть не может — оба привязаны к
// запросу, и выдумывать их для объекта без запроса запрещено.
func (s *PartnerSearchService) Object(ctx context.Context, objectID string) (ObjectPassport, error) {
	listing, err := s.listings.GetByExternalID(ctx, objectID)
	if err != nil {
		return ObjectPassport{}, apperr.ObjectNotFound()
	}
	return s.attachContact(ctx, buildStandalonePassport(listing), listing), nil
}

// Passport — карточка объекта в контексте поиска: с процентом совпадения и
// досье. Отсутствие объекта в выдаче этого поиска — не 404: партнёр мог
// прийти по прямой ссылке, и тогда он получит тот же объект «с витрины».
func (s *PartnerSearchService) Passport(ctx context.Context, partnerID, searchID uuid.UUID,
	objectID string) (ObjectPassport, error) {
	search, err := s.GetSearch(ctx, partnerID, searchID)
	if err != nil {
		return ObjectPassport{}, err
	}
	listing, err := s.listings.GetByExternalID(ctx, objectID)
	if err != nil {
		return ObjectPassport{}, apperr.ObjectNotFound()
	}
	res, err := s.searches.GetResult(ctx, searchID, objectID)
	if errors.Is(err, repository.ErrNotFound) {
		return s.attachContact(ctx, buildStandalonePassport(listing), listing), nil
	}
	if err != nil {
		return ObjectPassport{}, err
	}

	analysis := fallbackAnalysis(res.MatchScore, search.Explanation, res.AddressFacts)
	if search.City == "msk" && s.ml != nil {
		if dossier, ok := s.dossier(ctx, search, res); ok {
			analysis.Verdict = dossier.Verdict
			analysis.Brief = nonNilBrief(dossier.Brief)
			analysis.Blocks = nonNilBlocks(dossier.Blocks)
			analysis.Compromises = nonNilCompromises(dossier.Compromises)
			analysis.Relaxation = nonNilRelaxation(dossier.Relaxation)
			analysis.ZoneRationale = dossier.ZoneRationale
		}
	}
	p := staticPassport(listing)
	p.LifestyleAnalysis = analysis
	return s.attachContact(ctx, p, listing), nil
}

// dossier — тот же ленивый кэш, что в ObjectService: годное досье отдаётся из
// базы, протухшее держится наготове на случай, если ML не ответит. Отдать
// вчерашнее досье честнее, чем не отдать ничего, — но только если сегодняшнее
// получить не вышло.
func (s *PartnerSearchService) dossier(ctx context.Context, search domain.PartnerSearch,
	res domain.PartnerSearchResult) (DossierPayload, bool) {
	var stale DossierPayload
	haveStale := false
	if len(res.Dossier) > 0 && res.DossierVersion == DossierSchemaVersion {
		if payload, ok := decodeDossier(res.Dossier); ok {
			listingUpdated, _ := s.listings.GetUpdatedAt(ctx, res.ExternalID)
			if dossierFresh(res.DossierUpdatedAt, listingUpdated, s.ttlHours, time.Now()) {
				return payload, true
			}
			stale, haveStale = payload, true
		}
	}

	mlCtx, cancel := context.WithTimeout(ctx, s.dossierTTO)
	defer cancel()
	callStart := time.Now()
	resp, err := s.ml.Dossier(mlCtx, client.DossierRequest{
		ObjectID: res.ExternalID, City: search.City, RawQuery: search.Query,
		ParsedQuery: search.ParsedQuery, Relaxed: nonNilStrings(search.Relaxed),
		Degraded: nonNilStrings(search.Degraded),
	})
	observability.Default.ObserveMLCall("partner_dossier", time.Since(callStart).Seconds())
	if err != nil || resp == nil {
		return stale, haveStale
	}
	payload, ok := decodeDossier(resp.Dossier)
	if !ok {
		return stale, haveStale
	}
	version := resp.SchemaVersion
	if version == "" {
		version = DossierSchemaVersion
	}
	_ = s.searches.SaveDossier(ctx, search.ID, res.ExternalID, version, resp.Dossier)
	return payload, true
}

// PartnerAnswer — ответ на вопрос по объекту. Предложения возвращаются
// раздельно, вместе со ссылками на источники: интеграции нужен не абзац
// текста, а проверяемые утверждения — какое из них на чём основано.
type PartnerAnswer struct {
	Answer    string                    `json:"answer"`
	Sentences []client.GroundedSentence `json:"sentences"`
}

// Ask — вопрос по объекту, привязанный к поиску. Ответ строится только по
// досье и фактам объекта: чего в них нет, того в ответе не будет.
func (s *PartnerSearchService) Ask(ctx context.Context, partnerID, searchID uuid.UUID,
	objectID, question string) (PartnerAnswer, error) {
	search, err := s.GetSearch(ctx, partnerID, searchID)
	if err != nil {
		return PartnerAnswer{}, err
	}
	passport, err := s.Passport(ctx, partnerID, searchID, objectID)
	if err != nil {
		return PartnerAnswer{}, err
	}
	passportJSON, err := json.Marshal(passport)
	if err != nil {
		return PartnerAnswer{}, apperr.Internal("не удалось подготовить досье объекта")
	}
	var passportMap map[string]any
	_ = json.Unmarshal(passportJSON, &passportMap)

	mlCtx, cancel := context.WithTimeout(ctx, s.askTTO)
	defer cancel()
	callStart := time.Now()
	resp, err := s.ml.AskObject(mlCtx, client.ObjectAskRequest{
		Question: question,
		Passport: passportMap,
		SearchContext: map[string]any{
			"search_id": searchID.String(), "object_id": objectID,
			"raw_query": search.Query, "parsed_query": search.ParsedQuery,
			"relaxed": nonNilStrings(search.Relaxed), "degraded": nonNilStrings(search.Degraded),
		},
	})
	observability.Default.ObserveMLCall("partner_object_ask", time.Since(callStart).Seconds())
	if err != nil {
		return PartnerAnswer{}, mlAppError(err, s.askTTO)
	}

	sentences := resp.Sentences
	if sentences == nil {
		sentences = []client.GroundedSentence{}
	}
	parts := make([]string, 0, len(sentences))
	for _, sentence := range sentences {
		if sentence.Text != "" {
			parts = append(parts, sentence.Text)
		}
	}
	answer := joinSentences(parts)
	if answer == "" {
		answer = "Не знаю по этому объекту: в досье нет подтверждённых данных для ответа."
	}
	return PartnerAnswer{Answer: answer, Sentences: sentences}, nil
}

func (s *PartnerSearchService) attachContact(ctx context.Context, p ObjectPassport,
	l domain.Listing) ObjectPassport {
	var owner domain.OwnerListing
	found := false
	if s.owners != nil {
		if o, err := s.owners.GetByExternalID(ctx, l.ExternalID); err == nil {
			owner, found = o, true
		}
	}
	p.Contact = BuildPassportContact(owner, found, l)
	return p
}

func joinSentences(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// parsedQueryMap приводит разбор запроса к тому же словарю, что уходит в базу
// и обратно в ML как prev_parsed. Промежуточный JSON, а не ручное копирование
// полей: список полей ParsedQuery принадлежит ML, и дублировать его здесь
// значит расходиться с ним при первом же изменении.
func parsedQueryMap(parsed client.ParsedQuery) map[string]any {
	b, err := json.Marshal(parsed)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// mlAppError — отказ ML в конверте REST. Таймаут отдаётся как 504, остальное
// как 502: для клиента это разные ситуации — первую лечит повтор, вторую нет.
func mlAppError(err error, budget time.Duration) *apperr.Error {
	fail := mapMLError(err, budget)
	status := http.StatusBadGateway
	if fail.Code == "search_timeout" || errors.Is(err, client.ErrTimeout) {
		status = http.StatusGatewayTimeout
	}
	if errors.Is(err, client.ErrUnavailable) {
		status = http.StatusServiceUnavailable
	}
	return apperr.New(status, fail.Code, fail.Message).
		WithCause(fail.Cause).WithHint(fail.Hint)
}
