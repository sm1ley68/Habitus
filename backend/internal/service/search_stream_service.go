// search_stream_service.go — the SSE orchestration behind
// POST /chats/{chat_id}/messages/stream. See plan §4 for the full design
// rationale (why an in-memory lock, why synthetic agent_status events, why
// exactly one terminal `done`, how disconnects are handled).
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/sse"
	"habitus-backend/internal/observability"
	"habitus-backend/internal/repository"
)

// resultPageSize — сколько объектов уходит в первую страницу final_result.
// ML может отдать до result_max_n (Task 6, сейчас 30), но карточками первого
// экрана показываем только первые resultPageSize — остальное из уже
// сохранённого пула поднимает «показать ещё», GET /chats/{id}/results (Task 7).
const resultPageSize = 10

// slowStageWarnMS — с какой стадии начинается предупреждение в лог. 15 с при
// типичном бюджете в 60–120 с: ещё не отказ, но уже сигнал, что на машине
// послабее тот же запрос упрётся в таймаут.
const slowStageWarnMS = 15_000

// slowestStage возвращает самую долгую стадию ответа ML и её длительность.
// Пустые timings дают ("", 0) — ML прислала ответ без разбивки, и сказать про
// стадии нечего.
func slowestStage(timings map[string]float64) (string, float64) {
	name, worst := "", 0.0
	for stage, ms := range timings {
		if ms > worst {
			name, worst = stage, ms
		}
	}
	return name, worst
}

type AgentStatusEvent struct {
	Agent   string `json:"agent"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type TextTokenEvent struct {
	Token string `json:"token"`
}

type ChatRenamedEvent struct {
	ChatID string `json:"chat_id"`
	Title  string `json:"title"`
}

// ErrorEvent — терминальное событие потока. Cause/Hint аддитивны и omitempty:
// фронт, который их не ждёт, продолжает работать по code+message, а пустое
// поле означает «улики нет», а не «улика — пустая строка».
type ErrorEvent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

func errorEvent(fail userFacingError) ErrorEvent {
	return ErrorEvent{Code: fail.Code, Message: fail.Message,
		Cause: fail.Cause, Hint: fail.Hint}
}

type FinalResultEvent struct {
	SuggestedAreasGeoJSON any                 `json:"suggested_areas_geojson"`
	Objects               []FinalResultObject `json:"objects"`
	DataFreshness         string              `json:"data_freshness"`
	AreaLabel             string              `json:"area_label"`
	// Intent — намерение реплики (Task 4, multi-turn чат). Аддитивное поле:
	// фронт, который его не ждёт, может просто игнорировать. omitempty — та же
	// честная семантика, что у колонки intent: ML не прислала намерение —
	// поля нет, а не пустая строка вместо него.
	Intent string `json:"intent,omitempty"`
	// Total/HasMore — Task 7: сколько объектов реально сохранено для этого
	// поиска (весь пул из ответа ML, не только показанная страница) и есть ли
	// за пределами Objects ещё что показать. Аддитивные поля — существующие
	// не трогаем, фронт без «показать ещё» может их игнорировать.
	Total   int  `json:"total"`
	HasMore bool `json:"has_more"`
	// Diagnostics — почему выдача пуста: сколько объектов оставалось после
	// каждой клаузы фильтра. ML присылает их только при нулевой выдаче,
	// omitempty сохраняет ту же семантику в событии: поля нет, а не пустой
	// список, который фронт принял бы за «диагностика посчитана и пуста».
	Diagnostics []client.ConstraintDiagnostic `json:"diagnostics,omitempty"`
}

// pickSuggestedAreas: реальная граница зоны (из ML) заменяет convex-hull результатов.
func pickSuggestedAreas(hull, zone any) any {
	if zone != nil {
		return zone
	}
	return hull
}

// chatSearchStore — часть ChatSearchRepo, нужная сервису: сохранить поиск,
// обновить снапшот результата и прочитать разбор последнего поиска чата
// (prev_parsed для multi-turn запроса к ML, Task 4). Обособленный интерфейс —
// чтобы подменить хранилище в тестах без реальной БД.
type chatSearchStore interface {
	InsertSearch(ctx context.Context, cs domain.ChatSearch) (uuid.UUID, error)
	UpsertResult(ctx context.Context, res domain.ChatSearchResult) error
	LastParsedQuery(ctx context.Context, chatID uuid.UUID) (map[string]any, error)
}

type SearchStreamService struct {
	chats          *repository.ChatRepo
	messages       *repository.MessageRepo
	searches       chatSearchStore
	listings       *repository.ListingRepo
	ml             *client.MLClient
	mlTimeout      time.Duration
	explainTimeout time.Duration

	mu       sync.Mutex
	inFlight map[uuid.UUID]struct{}
}

func NewSearchStreamService(
	chats *repository.ChatRepo,
	messages *repository.MessageRepo,
	searches chatSearchStore,
	listings *repository.ListingRepo,
	ml *client.MLClient,
	mlTimeout time.Duration,
	explainTimeout time.Duration,
) *SearchStreamService {
	return &SearchStreamService{
		chats: chats, messages: messages, searches: searches, listings: listings,
		ml: ml, mlTimeout: mlTimeout, explainTimeout: explainTimeout,
		inFlight: make(map[uuid.UUID]struct{}),
	}
}

// TotalBudget is the deadline for the whole Run (search + explanation stream +
// persistence) — generous slack on top of both ML sub-timeouts.
func (s *SearchStreamService) TotalBudget() time.Duration {
	return s.mlTimeout + s.explainTimeout + 30*time.Second
}

// TryLock returns true if the caller acquired the per-chat stream lock.
// In-memory map — correct for exactly one backend replica (this pass's
// deployment), not for horizontal scaling; see plan §4.
func (s *SearchStreamService) TryLock(chatID uuid.UUID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inFlight[chatID]; busy {
		return false
	}
	s.inFlight[chatID] = struct{}{}
	return true
}

func (s *SearchStreamService) Unlock(chatID uuid.UUID) {
	s.mu.Lock()
	delete(s.inFlight, chatID)
	s.mu.Unlock()
}

type mlOutcome struct {
	resp *client.SearchResponse
	err  error
}

// Run drives the whole event sequence for one search. It never returns an
// error to the caller — every failure path is itself an `error` SSE event (or,
// once the peer is gone, a silent early return). Callers must have already
// verified chat ownership and acquired the per-chat lock before calling this.
func (s *SearchStreamService) Run(ctx context.Context, chat domain.Chat, text string, point *client.PointConstraint, w *sse.Writer) {
	_ = s.chats.SetStreamActive(ctx, chat.ID, true)
	defer func() { _ = s.chats.SetStreamActive(ctx, chat.ID, false) }()

	userMsg, err := s.messages.Insert(ctx, chat.ID, "user", text, nil)
	if err != nil {
		_ = w.WriteEvent("error", ErrorEvent{Code: "db_error", Message: "Не удалось сохранить сообщение"})
		return
	}

	isFirstMessage := false
	if n, err := s.messages.CountByRole(ctx, chat.ID, "user"); err == nil {
		isFirstMessage = n == 1
	}

	req := s.buildSearchRequest(ctx, chat, text, point)

	mlCtx, cancelML := context.WithTimeout(ctx, s.mlTimeout)
	defer cancelML()

	resultCh := make(chan mlOutcome, 1)
	go func() {
		callStart := time.Now()
		resp, err := s.ml.Search(mlCtx, req)
		observability.Default.ObserveMLCall("search", time.Since(callStart).Seconds())
		resultCh <- mlOutcome{resp: resp, err: err}
	}()

	scripted := []AgentStatusEvent{
		{Agent: "linguistic", Status: "processing", Message: "Разбираю запрос…"},
		{Agent: "geo", Status: "processing", Message: "Строю маршруты и считаю расстояния…"},
		{Agent: "context", Status: "processing", Message: "Сканирую структуру жилого фонда…"},
	}

	var outcome mlOutcome
	gotResult := false
	for _, ev := range scripted {
		if !s.emit(w, "agent_status", ev) {
			return
		}
		select {
		case outcome = <-resultCh:
			gotResult = true
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			_ = w.WriteEvent("error", errorEvent(mapMLError(client.ErrTimeout, s.mlTimeout)))
			return
		}
		if gotResult {
			break
		}
	}
	if !gotResult {
		// ml считает ответ 15–60с, всё это время в поток ничего не идёт.
		// Простаивающее SSE-соединение рвут прокси / VPN / антивирусы, поэтому
		// шлём keep-alive-комментарий раз в 2с, пока ждём результат.
		heartbeat := time.NewTicker(2 * time.Second)
		defer heartbeat.Stop()
		for !gotResult {
			select {
			case outcome = <-resultCh:
				gotResult = true
			case <-heartbeat.C:
				if err := w.WriteComment("keep-alive"); err != nil {
					return // клиент отвалился
				}
			case <-ctx.Done():
				_ = w.WriteEvent("error", errorEvent(mapMLError(client.ErrTimeout, s.mlTimeout)))
				return
			}
		}
	}

	if outcome.err != nil {
		fail := mapMLError(outcome.err, s.mlTimeout)
		// Пользователю уходит человеческий текст, в лог — исходная ошибка со
		// всеми потрохами: без неё «поиск не уложился» неотличимо от «ML отвечал
		// не туда», и разбираться приходится вслепую.
		log.Error().Err(outcome.err).
			Str("chat_id", chat.ID.String()).
			Str("code", fail.Code).
			Str("cause", fail.Cause).
			Dur("ml_budget", s.mlTimeout).
			Msg("search failed")
		_ = w.WriteEvent("error", errorEvent(fail))
		return
	}
	resp := outcome.resp

	// Медленный, но успешный ответ — предвестник таймаута у того, чья машина
	// слабее. Пишем разбивку по стадиям, пока она есть: после обрыва по
	// таймауту её уже никто не увидит, а именно она называет виновника.
	if slowest, ms := slowestStage(resp.Timings); ms >= slowStageWarnMS {
		log.Warn().
			Str("chat_id", chat.ID.String()).
			Str("stage", slowest).
			Float64("stage_ms", ms).
			Interface("timings_ms", resp.Timings).
			Msg("ML stage is slow — a weaker machine will hit the search timeout")
	}

	// habitus_ml_stage_seconds / habitus_ml_degraded_total (Task 8): считаем
	// ровно то, что реально прислала ML в этом ответе — timings отдаёт мс
	// только по выполнившимся стадиям (Task 1), degraded здесь читаем ДО
	// того, как ниже к нему может добавиться "llm" за счёт отдельного отказа
	// уже на стороне backend'а (см. withDegradation) — это не то, что вернула
	// ML, публиковать как её деградацию нельзя.
	for stage, ms := range resp.Timings {
		observability.Default.ObserveMLStage(stage, ms/1000)
	}
	for _, layer := range resp.Degraded {
		observability.Default.IncMLDegraded(layer)
	}

	if len(resp.Relaxed) > 0 {
		if !s.emit(w, "agent_status", AgentStatusEvent{
			Agent: "orchestrator", Status: "relaxation_triggered",
			Message: strings.Join(resp.Relaxed, "; "),
		}) {
			return
		}
	}

	processingMsg := "Собираю ответ…"
	if len(resp.Degraded) > 0 {
		processingMsg += fmt.Sprintf(" (часть слоёв недоступна: %s)", strings.Join(resp.Degraded, ", "))
	}
	if !s.emit(w, "agent_status", AgentStatusEvent{Agent: "orchestrator", Status: "processing", Message: processingMsg}) {
		return
	}

	explanation := s.streamExplanation(ctx, text, resp, w)
	if !explanation.Alive {
		return
	}
	// текст ушёл пользователем токенами, но в историю чата и в результаты
	// поиска он должен лечь целиком — оттуда его читает GET /messages
	resp.Explanation = explanation.Text
	if !explanation.LLMOK {
		resp.Degraded = withDegradation(resp.Degraded, "llm")
	}

	if isFirstMessage {
		title := renameTitle(resp.Parsed, text)
		if _, err := s.chats.Rename(ctx, chat.ID, title); err == nil {
			if !s.emit(w, "chat_renamed", ChatRenamedEvent{ChatID: chat.ID.String(), Title: title}) {
				return
			}
		}
	}

	// Exactly one terminal `done`, at orchestrator — the frontend's stage
	// machine treats ANY agent_status{status:"done"} as "whole run finished"
	// (see plan context notes), so per-agent done events would end the loader
	// animation early.
	if !s.emit(w, "agent_status", AgentStatusEvent{Agent: "orchestrator", Status: "done", Message: ""}) {
		return
	}

	finalResult, objectIDs, matchScores := s.buildFinalResult(ctx, resp, point)
	if !s.emit(w, "final_result", finalResult) {
		return
	}

	s.persist(ctx, chat.ID, userMsg.ID, text, resp, objectIDs, matchScores)

	_ = w.WriteEvent("stream_end", struct{}{})
}

func (s *SearchStreamService) emit(w *sse.Writer, event string, data any) bool {
	return w.WriteEvent(event, data) == nil
}

// searchRequestFor собирает запрос к ML. Explain выключен намеренно: объяснение
// приходит отдельным потоком (streamExplanation), поэтому держать выдачу
// объектов ради второго вызова LLM незачем.
func searchRequestFor(chat domain.Chat, text string,
	point *client.PointConstraint) client.SearchRequest {
	return client.SearchRequest{
		Query: text, City: chat.City, Point: point, Explain: false,
	}
}

// buildSearchRequest дополняет searchRequestFor разбором предыдущего шага
// диалога (prev_parsed, Task 4) — читает последний поиск чата перед вызовом
// ML. Ошибка чтения не фатальна: логируем и идём без контекста предыдущего
// шага — деградация до поведения одиночного запроса, а не 500.
func (s *SearchStreamService) buildSearchRequest(ctx context.Context, chat domain.Chat,
	text string, point *client.PointConstraint) client.SearchRequest {
	req := searchRequestFor(chat, text, point)

	prevParsed, err := s.searches.LastParsedQuery(ctx, chat.ID)
	if err != nil {
		log.Error().Err(err).Str("chat_id", chat.ID.String()).
			Msg("не удалось прочитать предыдущий разбор чата — ищем без контекста предыдущего шага")
		return req
	}
	req.PrevParsed = prevParsed
	return req
}

// explanationOutcome — итог потокового объяснения: собранный текст (для истории
// чата), пришёл ли он от LLM и жив ли ещё SSE-поток к пользователю.
type explanationOutcome struct {
	Text  string
	LLMOK bool
	Alive bool
}

// streamExplanation льёт объяснение из ML в SSE токен за токеном.
//
// Объекты к этому моменту уже найдены, поэтому падение объяснения — деградация,
// а не обрыв ответа: пользователь получит карточки и честную пометку слоя.
func (s *SearchStreamService) streamExplanation(ctx context.Context, query string,
	resp *client.SearchResponse, w *sse.Writer) explanationOutcome {
	explainCtx, cancel := context.WithTimeout(ctx, s.explainTimeout)
	defer cancel()

	var text strings.Builder
	alive := true
	callStart := time.Now()
	llmOK, err := s.ml.ExplainStream(explainCtx, client.ExplainRequest{
		Query: query, Results: resp.Results, Relaxed: resp.Relaxed,
		Notes: resp.Notes,
	}, func(token string) bool {
		if !s.emit(w, "text_token", TextTokenEvent{Token: token}) {
			alive = false
			return false
		}
		text.WriteString(token)
		return true
	})
	observability.Default.ObserveMLCall("explain", time.Since(callStart).Seconds())
	if err != nil {
		llmOK = false
	}
	return explanationOutcome{Text: text.String(), LLMOK: llmOK, Alive: alive}
}

// withDegradation добавляет слой в список деградаций, не задваивая уже
// отмеченный: список едет и в персист, и в подпись под выдачей.
func withDegradation(degraded []string, layer string) []string {
	for _, d := range degraded {
		if d == layer {
			return degraded
		}
	}
	return append(degraded, layer)
}

// splitTokens нарезает готовый текст на псевдо-токены для потоков, где ответ
// приходит целиком (Q&A по объекту отдаёт предложения с evidence-путями).
func splitTokens(text string) []string {
	if text == "" {
		return nil
	}
	var tokens []string
	var cur strings.Builder
	curIsSpace := isSpaceRune(rune(text[0]))
	for _, r := range text {
		isSpace := isSpaceRune(r)
		if isSpace != curIsSpace && cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
			curIsSpace = isSpace
		}
		cur.WriteRune(r)
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

func isSpaceRune(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}

var geoKindRu = map[string]string{"school": "школы", "metro": "метро", "park": "парка"}
var stopFactorRu = map[string]string{"bars": "баров", "communal_flats": "коммуналок"}

// renameTitle is a rule-based title (no second LLM call this pass — see plan
// §4). Swappable for an LLM-generated title later behind the same
// `chat_renamed` event with zero frontend change.
func renameTitle(parsed client.ParsedQuery, rawText string) string {
	var parts []string
	if len(parsed.Geo) > 0 {
		g := parsed.Geo[0]
		kind := geoKindRu[g.Kind]
		if kind == "" {
			kind = g.Kind
		}
		parts = append(parts, fmt.Sprintf("Поиск у %s ≤%d мин", kind, g.WalkMinutes))
	}
	if len(parsed.Rooms) > 0 {
		roomsStr := make([]string, len(parsed.Rooms))
		for i, r := range parsed.Rooms {
			roomsStr[i] = fmt.Sprintf("%d", r)
		}
		parts = append(parts, strings.Join(roomsStr, "/")+"-комн")
	}
	if len(parsed.StopFactors) > 0 {
		label := stopFactorRu[parsed.StopFactors[0]]
		if label == "" {
			label = parsed.StopFactors[0]
		}
		parts = append(parts, "без "+label)
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	runes := []rune(strings.TrimSpace(rawText))
	if len(runes) > 40 {
		return string(runes[:40]) + "…"
	}
	if len(runes) == 0 {
		return "Новый поиск квартиры"
	}
	return string(runes)
}

func (s *SearchStreamService) buildFinalResult(ctx context.Context, resp *client.SearchResponse, pointConstraint *client.PointConstraint) (FinalResultEvent, []string, map[string]int) {
	ids := make([]string, len(resp.Results))
	for i, r := range resp.Results {
		ids[i] = r.ExternalID
	}
	listings, err := s.listings.GetByExternalIDs(ctx, ids)
	if err != nil {
		listings = map[string]domain.Listing{}
	}

	// all — весь набор, что ML вернул и что уходит на сохранение в
	// chat_search_results (Task 7); в событие final_result едет только первая
	// страница (paginateFinalResult), остальное поднимает «показать ещё».
	all := []FinalResultObject{}
	var coords [][2]float64
	for rank, item := range resp.Results {
		obj, ok := BuildFinalResultObject(item, rank, resp.Degraded, listings)
		if !ok {
			continue
		}
		all = append(all, obj)
		coords = append(coords, [2]float64{obj.Coordinates[0], obj.Coordinates[1]})
	}

	var customPoint *[2]float64
	if pointConstraint != nil {
		p := [2]float64{pointConstraint.Lon, pointConstraint.Lat}
		customPoint = &p
	}
	suggested := pickSuggestedAreas(BuildSuggestedAreas(coords, customPoint), resp.AreaGeojson)

	// проценты берутся ровно те, что уехали в выдачу, — паспорт обязан показать
	// то же число, а не пересчитывать его из сырого скора без ранга и degraded.
	// Считаем по ВСЕМУ сохранённому набору: «показать ещё» обязан увидеть тот
	// же процент, что уже был посчитан на первой странице.
	scores := make(map[string]int, len(all))
	for _, o := range all {
		scores[o.ID] = o.MatchScore
	}
	objectIDs := make([]string, len(all))
	for i, o := range all {
		objectIDs[i] = o.ID
	}

	shown, total, hasMore := paginateFinalResult(all)

	return FinalResultEvent{
		SuggestedAreasGeoJSON: suggested,
		Objects:               shown,
		DataFreshness:         resp.DataFreshness,
		AreaLabel:             resp.AreaLabel,
		Intent:                resp.Intent,
		Total:                 total,
		HasMore:               hasMore,
		Diagnostics:           resp.Diagnostics,
	}, objectIDs, scores
}

// paginateFinalResult режет собранный список объектов до первой страницы
// (resultPageSize) и считает total/has_more по полному набору — «показать
// ещё» дальше поднимает остальное через GET /chats/{id}/results (Task 7).
func paginateFinalResult(all []FinalResultObject) (shown []FinalResultObject, total int, hasMore bool) {
	total = len(all)
	if total <= resultPageSize {
		return all, total, false
	}
	return all[:resultPageSize], total, true
}

// historyObjectIDs — что кладём в meta ассистентского сообщения. Только первая
// страница: восстановленное из истории сообщение обязано показывать тот же
// первый экран, что пользователь видел в потоке, а не весь сохранённый пул
// (Task 7). Остальное поднимает «показать ещё» из chat_search_results.
func historyObjectIDs(objectIDs []string) []string {
	if len(objectIDs) > resultPageSize {
		return objectIDs[:resultPageSize]
	}
	return objectIDs
}

func (s *SearchStreamService) persist(ctx context.Context, chatID, userMsgID uuid.UUID, rawQuery string, resp *client.SearchResponse, objectIDs []string, matchScores map[string]int) {
	searchID, err := s.searches.InsertSearch(ctx, domain.ChatSearch{
		ChatID: chatID, MessageID: &userMsgID, RawQuery: rawQuery,
		ParsedQuery: parsedQueryToMap(resp.Parsed), Relaxed: resp.Relaxed,
		DataFreshness: resp.DataFreshness, Degraded: resp.Degraded,
		Intent: intentPtr(resp.Intent),
	})
	if err != nil {
		return
	}

	shown := make(map[string]bool, len(objectIDs))
	for _, id := range objectIDs {
		shown[id] = true
	}
	for _, item := range resp.Results {
		if !shown[item.ExternalID] {
			continue // wasn't rendered (listing missing) — don't persist an unshown result
		}
		_ = s.searches.UpsertResult(ctx, domain.ChatSearchResult{
			ChatID: chatID, ExternalID: item.ExternalID, SearchID: searchID,
			Price: item.Price, Area: item.Area, Rooms: item.Rooms,
			AddressFacts: item.AddressFacts, Score: item.Score,
			MatchScore: matchScores[item.ExternalID], Explanation: resp.Explanation,
		})
	}

	meta := map[string]any{"suggested_object_ids": historyObjectIDs(objectIDs)}
	_, _ = s.messages.Insert(ctx, chatID, "assistant", resp.Explanation, meta)
	_ = s.chats.Touch(ctx, chatID)
}

// intentPtr — пустая строка от ML равнозначна отсутствию intent в ответе:
// колонка chat_searches.intent обязана остаться NULL, а не получить "".
func intentPtr(intent string) *string {
	if intent == "" {
		return nil
	}
	return &intent
}

func parsedQueryToMap(pq client.ParsedQuery) map[string]any {
	b, err := json.Marshal(pq)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
