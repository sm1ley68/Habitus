package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/client"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/sse"
)

func TestPickSuggestedAreas(t *testing.T) {
	hull := map[string]any{"type": "FeatureCollection", "features": []any{"hull"}}
	zone := map[string]any{"type": "FeatureCollection", "features": []any{"zone"}}

	// зона есть → она вытесняет hull
	if got := pickSuggestedAreas(hull, zone); got == nil ||
		got.(map[string]any)["features"].([]any)[0] != "zone" {
		t.Fatalf("зона должна заменить hull, получили %v", got)
	}
	// зоны нет → остаётся hull
	if got := pickSuggestedAreas(hull, nil); got.(map[string]any)["features"].([]any)[0] != "hull" {
		t.Fatalf("без зоны должен остаться hull, получили %v", got)
	}
}

// --- потоковое объяснение ---------------------------------------------

func explainStub(t *testing.T, frames ...string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		for _, f := range frames {
			_, _ = fmt.Fprint(w, f)
			flusher.Flush()
		}
	}))
}

func tokenFrame(token string) string {
	return fmt.Sprintf("event: token\ndata: {\"token\":%q}\n\n", token)
}

func doneFrame(llmOK bool) string {
	return fmt.Sprintf("event: done\ndata: {\"llm_ok\":%t}\n\n", llmOK)
}

// sseSink — writer, в который пишет sse.Writer, плюс собранный им текст.
func sseSink() (*sse.Writer, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return sse.New(bufio.NewWriter(buf)), buf
}

type deadWriter struct{}

func (deadWriter) Write([]byte) (int, error) { return 0, errors.New("клиент отвалился") }

func streamServiceWithML(url string) *SearchStreamService {
	return NewSearchStreamService(nil, nil, nil, nil,
		client.NewMLClient(url, time.Second), time.Second, 5*time.Second)
}

func TestStreamExplanationEmitsTokensAsTheyArrive(t *testing.T) {
	server := explainStub(t, tokenFrame("Тихая "), tokenFrame("двушка."), doneFrame(true))
	defer server.Close()

	w, buf := sseSink()
	got := streamServiceWithML(server.URL).streamExplanation(
		context.Background(), "тихо", &client.SearchResponse{}, w)

	if !got.Alive || !got.LLMOK {
		t.Fatalf("outcome = %#v; want живой поток от LLM", got)
	}
	if got.Text != "Тихая двушка." {
		t.Fatalf("Text = %q; want %q", got.Text, "Тихая двушка.")
	}
	// каждый токен ушёл отдельным кадром text_token, а не одним куском
	if n := strings.Count(buf.String(), "event: text_token"); n != 2 {
		t.Fatalf("кадров text_token = %d; want 2 (%s)", n, buf.String())
	}
}

func TestStreamExplanationKeepsTextForHistory(t *testing.T) {
	// Текст ложится в chat-историю: если после стрима его не собрать,
	// ассистентское сообщение сохранится пустым.
	server := explainStub(t, tokenFrame("Ответ."), doneFrame(true))
	defer server.Close()

	w, _ := sseSink()
	got := streamServiceWithML(server.URL).streamExplanation(
		context.Background(), "q", &client.SearchResponse{}, w)

	if got.Text != "Ответ." {
		t.Fatalf("Text = %q; текст обязан пережить стрим для истории чата", got.Text)
	}
}

func TestStreamExplanationSurvivesMLFailure(t *testing.T) {
	// ML недоступен: объекты уже найдены и должны доехать до пользователя,
	// поэтому это деградация, а не обрыв всего ответа.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	w, _ := sseSink()
	got := streamServiceWithML(server.URL).streamExplanation(
		context.Background(), "q", &client.SearchResponse{}, w)

	if !got.Alive {
		t.Fatalf("Alive = false; падение объяснения не должно рвать поток")
	}
	if got.LLMOK {
		t.Fatalf("LLMOK = true; want false")
	}
}

func TestStreamExplanationStopsWhenClientIsGone(t *testing.T) {
	server := explainStub(t, tokenFrame("раз"), tokenFrame("два"), doneFrame(true))
	defer server.Close()

	w := sse.New(bufio.NewWriterSize(deadWriter{}, 1))
	got := streamServiceWithML(server.URL).streamExplanation(
		context.Background(), "q", &client.SearchResponse{}, w)

	if got.Alive {
		t.Fatalf("Alive = true; запись в мёртвого клиента должна остановить поток")
	}
}

func TestWithDegradationAppendsOnceAndKeepsOrder(t *testing.T) {
	got := withDegradation([]string{"nlu"}, "llm")
	if len(got) != 2 || got[0] != "nlu" || got[1] != "llm" {
		t.Fatalf("withDegradation = %v; want [nlu llm]", got)
	}
	if again := withDegradation(got, "llm"); len(again) != 2 {
		t.Fatalf("withDegradation = %v; повторный слой не должен дублироваться", again)
	}
}

// --- prev_parsed (Task 4, многоходовый чат) -----------------------------

// fakeChatSearchStore — подставная реализация chatSearchStore: без реальной
// БД проверяем, как сервис реагирует на разные ответы хранилища.
type fakeChatSearchStore struct {
	lastParsed map[string]any
	lastErr    error
}

func (f fakeChatSearchStore) InsertSearch(context.Context, domain.ChatSearch) (uuid.UUID, error) {
	return uuid.Nil, nil
}
func (f fakeChatSearchStore) UpsertResult(context.Context, domain.ChatSearchResult) error {
	return nil
}
func (f fakeChatSearchStore) LastParsedQuery(context.Context, uuid.UUID) (map[string]any, error) {
	return f.lastParsed, f.lastErr
}

func TestBuildSearchRequestIncludesPrevParsedFromStore(t *testing.T) {
	prev := map[string]any{"semantic_text": "тихо"}
	svc := &SearchStreamService{searches: fakeChatSearchStore{lastParsed: prev}}

	got := svc.buildSearchRequest(context.Background(), domain.Chat{ID: uuid.New()}, "а подешевле", nil)

	if got.PrevParsed == nil || got.PrevParsed["semantic_text"] != "тихо" {
		t.Fatalf("PrevParsed = %#v; want %#v", got.PrevParsed, prev)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(b), `"prev_parsed"`) {
		t.Fatalf("тело запроса не содержит prev_parsed: %s", b)
	}
}

func TestBuildSearchRequestOmitsPrevParsedOnFirstSearch(t *testing.T) {
	// Поисков в чате ещё не было — хранилище отдаёт «нет данных» без ошибки.
	svc := &SearchStreamService{searches: fakeChatSearchStore{}}

	got := svc.buildSearchRequest(context.Background(), domain.Chat{ID: uuid.New()}, "квартира", nil)

	if got.PrevParsed != nil {
		t.Fatalf("PrevParsed = %#v; want nil на первом поиске в чате", got.PrevParsed)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(b), `"prev_parsed"`) {
		t.Fatalf("тело запроса содержит prev_parsed на первом поиске: %s", b)
	}
}

func TestBuildSearchRequestSurvivesStoreError(t *testing.T) {
	// Ошибка чтения прошлого разбора не фатальна: поиск идёт без контекста
	// предыдущего шага, а не падает.
	svc := &SearchStreamService{searches: fakeChatSearchStore{lastErr: errors.New("бд моргнула")}}

	got := svc.buildSearchRequest(context.Background(), domain.Chat{ID: uuid.New(), City: "msk"}, "квартира", nil)

	if got.PrevParsed != nil {
		t.Fatalf("PrevParsed = %#v; want nil при ошибке чтения", got.PrevParsed)
	}
	if got.Query != "квартира" || got.City != "msk" {
		t.Fatalf("запрос не должен пострадать от ошибки чтения контекста: %#v", got)
	}
}

// --- intent (Task 4) -----------------------------------------------------

func TestBuildFinalResultCarriesIntentFromResponse(t *testing.T) {
	// svc без listings допустим только потому, что resp.Results пуст:
	// buildFinalResult не дойдёт до обращения к репозиторию. Тесту с непустыми
	// Results понадобится настоящий (или подставной) listings.
	svc := &SearchStreamService{}
	resp := &client.SearchResponse{Intent: "refine"}

	final, _, _ := svc.buildFinalResult(context.Background(), resp, nil)

	if final.Intent != "refine" {
		t.Fatalf("Intent = %q; want %q", final.Intent, "refine")
	}
	b, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if got["intent"] != "refine" {
		t.Fatalf("intent в final_result = %#v; want %q", got["intent"], "refine")
	}
}

func TestBuildFinalResultOmitsIntentWhenMLSentNone(t *testing.T) {
	// Отсутствие намерения не подменяется пустой строкой — поля просто нет.
	svc := &SearchStreamService{}

	final, _, _ := svc.buildFinalResult(context.Background(),
		&client.SearchResponse{}, nil)

	b, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := got["intent"]; ok {
		t.Fatalf("intent присутствует в final_result = %#v; want поле отсутствует", got["intent"])
	}
}

// --- пагинация final_result / «показать ещё» (Task 7) --------------------

func makeFinalResultObjects(n int) []FinalResultObject {
	objs := make([]FinalResultObject, n)
	for i := range objs {
		objs[i] = FinalResultObject{ID: fmt.Sprintf("obj-%d", i)}
	}
	return objs
}

func TestPaginateFinalResultTruncatesAndSignalsHasMore(t *testing.T) {
	// ML вернула больше объектов, чем показывает первая страница (Task 6:
	// result_max_n=30) — событие обязано унести только первые resultPageSize
	// и честно сказать, что осталось ещё.
	all := makeFinalResultObjects(23)

	shown, total, hasMore := paginateFinalResult(all)

	if len(shown) != resultPageSize {
		t.Fatalf("len(shown) = %d; want %d", len(shown), resultPageSize)
	}
	if total != 23 {
		t.Fatalf("total = %d; want 23", total)
	}
	if !hasMore {
		t.Fatal("hasMore = false; want true — сохранено больше, чем показано")
	}
	if shown[0].ID != all[0].ID || shown[len(shown)-1].ID != all[resultPageSize-1].ID {
		t.Fatalf("shown должен быть точным префиксом all: %#v", shown)
	}
}

func TestPaginateFinalResultNoTruncationWhenFits(t *testing.T) {
	all := makeFinalResultObjects(7)

	shown, total, hasMore := paginateFinalResult(all)

	if len(shown) != 7 {
		t.Fatalf("len(shown) = %d; want 7 (весь набор помещается на одну страницу)", len(shown))
	}
	if total != 7 {
		t.Fatalf("total = %d; want 7", total)
	}
	if hasMore {
		t.Fatal("hasMore = true; want false — больше нечего показывать")
	}
}

func TestBuildFinalResultEventCarriesTotalAndHasMoreThroughJSON(t *testing.T) {
	// final_result — не только Go-структура: контракт живёт в JSON-теле SSE,
	// проверяем, что total/has_more реально уезжают наружу под нужными ключами.
	svc := &SearchStreamService{}
	final, _, _ := svc.buildFinalResult(context.Background(), &client.SearchResponse{}, nil)

	b, err := json.Marshal(final)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if _, ok := got["total"]; !ok {
		t.Fatalf("total отсутствует в final_result: %s", b)
	}
	if _, ok := got["has_more"]; !ok {
		t.Fatalf("has_more отсутствует в final_result: %s", b)
	}
}

func TestSearchRequestSkipsSynchronousExplanation(t *testing.T) {
	// Пара к ExplainStream: /search обязан вернуть объекты, не дожидаясь
	// второго вызова LLM — текст придёт потоком следом.
	got := searchRequestFor(domain.Chat{City: "msk"}, "тихо", nil)

	if got.Explain {
		t.Fatalf("Explain = true; объяснение забирается отдельным потоком")
	}
	if got.Query != "тихо" || got.City != "msk" {
		t.Fatalf("request = %#v", got)
	}
}

func TestHistoryObjectIDsKeepsOnlyFirstPage(t *testing.T) {
	// В chat_search_results уходит весь пул, но история чата обязана
	// восстанавливать тот же первый экран, что видел пользователь в потоке.
	all := makeFinalResultObjects(23)
	ids := make([]string, len(all))
	for i, o := range all {
		ids[i] = o.ID
	}

	got := historyObjectIDs(ids)

	if len(got) != resultPageSize {
		t.Fatalf("len = %d; want %d", len(got), resultPageSize)
	}
	if got[0] != ids[0] || got[len(got)-1] != ids[resultPageSize-1] {
		t.Fatalf("meta должна быть точным префиксом выдачи: %v", got)
	}
}

func TestHistoryObjectIDsKeepsShortListIntact(t *testing.T) {
	ids := []string{"A", "B", "C"}
	if got := historyObjectIDs(ids); len(got) != 3 {
		t.Fatalf("len = %d; want 3 — короткий список режется зря", len(got))
	}
}
