// search_stream_service.go — the SSE orchestration behind
// POST /chats/{chat_id}/messages/stream. See plan §4 for the full design
// rationale (why an in-memory lock, why synthetic agent_status events, why
// exactly one terminal `done`, how disconnects are handled).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/sse"
	"habitus-backend/internal/repository"
)

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

type ErrorEvent struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type FinalResultEvent struct {
	SuggestedAreasGeoJSON any                 `json:"suggested_areas_geojson"`
	Objects               []FinalResultObject `json:"objects"`
	DataFreshness         string              `json:"data_freshness"`
	AreaLabel             string              `json:"area_label"`
	// Intent — намерение реплики (Task 4, multi-turn чат). Аддитивное поле:
	// фронт, который его не ждёт, может просто игнорировать.
	Intent string `json:"intent"`
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
		resp, err := s.ml.Search(mlCtx, req)
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
			_ = w.WriteEvent("error", ErrorEvent{Code: "llm_timeout", Message: "Не удалось получить ответ от ИИ, попробуйте ещё раз"})
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
				_ = w.WriteEvent("error", ErrorEvent{Code: "llm_timeout", Message: "Не удалось получить ответ от ИИ, попробуйте ещё раз"})
				return
			}
		}
	}

	if outcome.err != nil {
		code, msg := mapMLError(outcome.err)
		_ = w.WriteEvent("error", ErrorEvent{Code: code, Message: msg})
		return
	}
	resp := outcome.resp

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

func mapMLError(err error) (code, message string) {
	switch {
	case errors.Is(err, client.ErrTimeout):
		return "llm_timeout", "Не удалось получить ответ от ИИ, попробуйте ещё раз"
	case errors.Is(err, client.ErrUnavailable):
		return "llm_unavailable", "Сервис поиска временно недоступен"
	case errors.Is(err, client.ErrServer):
		return "db_error", "Ошибка на стороне сервиса поиска"
	default:
		return "internal_error", "Внутренняя ошибка сервера"
	}
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
	llmOK, err := s.ml.ExplainStream(explainCtx, client.ExplainRequest{
		Query: query, Results: resp.Results, Relaxed: resp.Relaxed,
	}, func(token string) bool {
		if !s.emit(w, "text_token", TextTokenEvent{Token: token}) {
			alive = false
			return false
		}
		text.WriteString(token)
		return true
	})
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

	objects := []FinalResultObject{}
	var coords [][2]float64
	for rank, item := range resp.Results {
		obj, ok := BuildFinalResultObject(item, rank, resp.Degraded, listings)
		if !ok {
			continue
		}
		objects = append(objects, obj)
		coords = append(coords, [2]float64{obj.Coordinates[0], obj.Coordinates[1]})
	}

	var customPoint *[2]float64
	if pointConstraint != nil {
		p := [2]float64{pointConstraint.Lon, pointConstraint.Lat}
		customPoint = &p
	}
	suggested := pickSuggestedAreas(BuildSuggestedAreas(coords, customPoint), resp.AreaGeojson)

	// проценты берутся ровно те, что уехали в выдачу, — паспорт обязан показать
	// то же число, а не пересчитывать его из сырого скора без ранга и degraded
	scores := make(map[string]int, len(objects))
	for _, o := range objects {
		scores[o.ID] = o.MatchScore
	}
	objectIDs := make([]string, len(objects))
	for i, o := range objects {
		objectIDs[i] = o.ID
	}

	return FinalResultEvent{
		SuggestedAreasGeoJSON: suggested,
		Objects:               objects,
		DataFreshness:         resp.DataFreshness,
		AreaLabel:             resp.AreaLabel,
		Intent:                resp.Intent,
	}, objectIDs, scores
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

	meta := map[string]any{"suggested_object_ids": objectIDs}
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
