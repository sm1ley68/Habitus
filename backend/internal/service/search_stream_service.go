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
}

type SearchStreamService struct {
	chats     *repository.ChatRepo
	messages  *repository.MessageRepo
	searches  *repository.ChatSearchRepo
	listings  *repository.ListingRepo
	ml        *client.MLClient
	mlTimeout time.Duration

	mu       sync.Mutex
	inFlight map[uuid.UUID]struct{}
}

func NewSearchStreamService(
	chats *repository.ChatRepo,
	messages *repository.MessageRepo,
	searches *repository.ChatSearchRepo,
	listings *repository.ListingRepo,
	ml *client.MLClient,
	mlTimeout time.Duration,
) *SearchStreamService {
	return &SearchStreamService{
		chats: chats, messages: messages, searches: searches, listings: listings,
		ml: ml, mlTimeout: mlTimeout, inFlight: make(map[uuid.UUID]struct{}),
	}
}

// TotalBudget is the deadline for the whole Run (ML wait + token streaming +
// persistence) — generous slack on top of the ML sub-timeout.
func (s *SearchStreamService) TotalBudget() time.Duration {
	return s.mlTimeout + 30*time.Second
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

	mlCtx, cancelML := context.WithTimeout(ctx, s.mlTimeout)
	defer cancelML()

	resultCh := make(chan mlOutcome, 1)
	go func() {
		resp, err := s.ml.Search(mlCtx, client.SearchRequest{Query: text, Point: point})
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

	for _, tok := range splitTokens(resp.Explanation) {
		if !s.emit(w, "text_token", TextTokenEvent{Token: tok}) {
			return
		}
		time.Sleep(28 * time.Millisecond)
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

	finalResult, objectIDs := s.buildFinalResult(ctx, resp, point)
	if !s.emit(w, "final_result", finalResult) {
		return
	}

	s.persist(ctx, chat.ID, userMsg.ID, text, resp, objectIDs)

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

// splitTokens mirrors the frontend's own mock streaming split (text.split(/(\s+)/))
// so real and mock streams feel identical.
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

func (s *SearchStreamService) buildFinalResult(ctx context.Context, resp *client.SearchResponse, pointConstraint *client.PointConstraint) (FinalResultEvent, []string) {
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
	areas := BuildSuggestedAreas(coords, customPoint)

	objectIDs := make([]string, len(objects))
	for i, o := range objects {
		objectIDs[i] = o.ID
	}

	return FinalResultEvent{
		SuggestedAreasGeoJSON: areas,
		Objects:               objects,
		DataFreshness:         resp.DataFreshness,
	}, objectIDs
}

func (s *SearchStreamService) persist(ctx context.Context, chatID, userMsgID uuid.UUID, rawQuery string, resp *client.SearchResponse, objectIDs []string) {
	searchID, err := s.searches.InsertSearch(ctx, domain.ChatSearch{
		ChatID: chatID, MessageID: &userMsgID, RawQuery: rawQuery,
		ParsedQuery: parsedQueryToMap(resp.Parsed), Relaxed: resp.Relaxed,
		DataFreshness: resp.DataFreshness, Degraded: resp.Degraded,
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
			AddressFacts: item.AddressFacts, Score: item.Score, Explanation: resp.Explanation,
		})
	}

	meta := map[string]any{"suggested_object_ids": objectIDs}
	_, _ = s.messages.Insert(ctx, chatID, "assistant", resp.Explanation, meta)
	_ = s.chats.Touch(ctx, chatID)
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
