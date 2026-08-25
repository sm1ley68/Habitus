package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestMLClientHealthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("путь = %q, ожидался /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	if err := NewMLClient(srv.URL, time.Second).Health(context.Background()); err != nil {
		t.Fatalf("Health вернул ошибку на живом сервисе: %v", err)
	}
}

func TestMLClientHealthFailsOnBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := NewMLClient(srv.URL, time.Second).Health(context.Background()); err == nil {
		t.Fatal("Health = nil на 503, ожидалась ошибка")
	}
}

func testMLServer(t *testing.T, requests chan<- SearchRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req SearchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests <- req
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[],"explanation":"","parsed":{},"relaxed":[],"data_freshness":"нет данных","degraded":[]}`)
	}))
}

func TestSearchOmitsPointWhenAbsent(t *testing.T) {
	requests := make(chan SearchRequest, 1)
	server := testMLServer(t, requests)
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(), SearchRequest{Query: "тихо"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got := <-requests; got.Point != nil {
		t.Fatalf("Search() point = %#v; want nil", got.Point)
	}
}

func TestSearchSendsPoint(t *testing.T) {
	requests := make(chan SearchRequest, 1)
	server := testMLServer(t, requests)
	defer server.Close()

	want := &PointConstraint{Lon: 37.6, Lat: 55.7, Minutes: 15, Mode: "foot-walking"}
	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(), SearchRequest{Query: "рядом", Point: want}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	got := (<-requests).Point
	if got == nil || *got != *want {
		t.Fatalf("Search() point = %#v; want %#v", got, want)
	}
}

func TestSearchOmitsPrevParsedWhenAbsent(t *testing.T) {
	// Первый поиск в чате: PrevParsed не задан — omitempty должен убрать поле
	// из тела запроса целиком, а не отправить null.
	// Тело читаем через канал, а не через общую переменную: сокет не даёт
	// happens-before между горутиной обработчика и горутиной теста.
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[],"explanation":"","parsed":{},"data_freshness":""}`)
	}))
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(), SearchRequest{Query: "тихо"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	raw := <-bodies
	if _, ok := raw["prev_parsed"]; ok {
		t.Fatalf("prev_parsed присутствует в теле = %v; want поле отсутствует", raw["prev_parsed"])
	}
}

func TestSearchSendsPrevParsedWhenPresent(t *testing.T) {
	// Многоходовый чат: разбор предыдущего шага должен доехать до ML целиком.
	requests := make(chan SearchRequest, 1)
	server := testMLServer(t, requests)
	defer server.Close()

	want := map[string]any{"rooms": []any{2.0}, "semantic_text": "тихо"}
	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(),
		SearchRequest{Query: "а без первого этажа", PrevParsed: want}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	got := (<-requests).PrevParsed
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("PrevParsed = %#v; want %#v", got, want)
	}
}

func TestSearchDecodesIntentFromResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[],"explanation":"","parsed":{},"data_freshness":"","intent":"refine"}`)
	}))
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	resp, err := c.Search(context.Background(), SearchRequest{Query: "а подешевле"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if resp.Intent != "refine" {
		t.Fatalf("Intent = %q; want %q", resp.Intent, "refine")
	}
}

func TestDossierAndObjectAskUseInternalContracts(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/dossier":
			var req DossierRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.City != "msk" || req.ObjectID != "E1" {
				t.Errorf("dossier request = %#v", req)
			}
			_, _ = fmt.Fprint(w, `{"dossier":{"verdict":{"headline":"ok","confidence":1,"layers_checked":1},"brief":[],"blocks":[],"compromises":[],"relaxation":[],"zone_rationale":""},"schema_version":"dossier-v1"}`)
		case "/object-ask":
			var req ObjectAskRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Question != "Почему?" {
				t.Errorf("ask request = %#v", req)
			}
			_, _ = fmt.Fprint(w, `{"sentences":[{"text":"Потому.","evidence_paths":["$.id"],"unknown":false}]}`)
		}
	}))
	defer server.Close()
	client := NewMLClient(server.URL, time.Second)
	dossier, err := client.Dossier(context.Background(), DossierRequest{ObjectID: "E1", City: "msk"})
	if err != nil || dossier.SchemaVersion != "dossier-v1" {
		t.Fatalf("Dossier() = %#v, %v", dossier, err)
	}
	answer, err := client.AskObject(context.Background(), ObjectAskRequest{
		Question: "Почему?", Passport: map[string]any{"id": "E1"},
	})
	if err != nil || answer.Sentences[0].Text != "Потому." {
		t.Fatalf("AskObject() = %#v, %v", answer, err)
	}
	if len(paths) != 2 || paths[0] != "/dossier" || paths[1] != "/object-ask" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSearchSendsCity(t *testing.T) {
	requests := make(chan SearchRequest, 1)
	server := testMLServer(t, requests)
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(), SearchRequest{Query: "тихо", City: "spb"}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got := <-requests; got.City != "spb" {
		t.Fatalf("Search() city = %q; want %q", got.City, "spb")
	}
}

// --- /explain/stream ---------------------------------------------------

// explainServer отдаёт заранее заданные SSE-кадры с паузой перед каждым.
func explainServer(t *testing.T, frames []string, pause time.Duration,
	captured chan<- ExplainRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			var req ExplainRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode request: %v", err)
			}
			captured <- req
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("test server does not support flushing")
		}
		for _, frame := range frames {
			time.Sleep(pause)
			_, _ = fmt.Fprint(w, frame)
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

func TestExplainStreamDeliversTokensInOrder(t *testing.T) {
	server := explainServer(t, []string{
		tokenFrame("Тихая "), tokenFrame("двушка."), doneFrame(true)}, 0, nil)
	defer server.Close()

	var got []string
	c := NewMLClient(server.URL, time.Second)
	llmOK, err := c.ExplainStream(context.Background(), ExplainRequest{Query: "тихо"},
		func(tok string) bool { got = append(got, tok); return true })

	if err != nil {
		t.Fatalf("ExplainStream() error = %v", err)
	}
	if !llmOK {
		t.Fatalf("llmOK = false; want true")
	}
	if strings.Join(got, "") != "Тихая двушка." {
		t.Fatalf("tokens = %q; want %q", strings.Join(got, ""), "Тихая двушка.")
	}
}

func TestExplainStreamReportsDegradedGeneration(t *testing.T) {
	server := explainServer(t, []string{tokenFrame("шаблон"), doneFrame(false)}, 0, nil)
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	llmOK, err := c.ExplainStream(context.Background(), ExplainRequest{Query: "тихо"},
		func(string) bool { return true })

	if err != nil {
		t.Fatalf("ExplainStream() error = %v", err)
	}
	if llmOK {
		t.Fatalf("llmOK = true; want false — ML отдал деградацию")
	}
}

func TestExplainStreamSendsQueryResultsAndRelaxations(t *testing.T) {
	captured := make(chan ExplainRequest, 1)
	server := explainServer(t, []string{doneFrame(true)}, 0, captured)
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	_, err := c.ExplainStream(context.Background(), ExplainRequest{
		Query:   "тихая двушка",
		Results: []ResultItem{{ExternalID: "A", Score: 0.9}},
		Relaxed: []string{"бюджет +15%"},
		Notes:   []string{"ориентация окон известна у 1.9% объявлений"},
	}, func(string) bool { return true })
	if err != nil {
		t.Fatalf("ExplainStream() error = %v", err)
	}

	got := <-captured
	if got.Query != "тихая двушка" || len(got.Results) != 1 ||
		got.Results[0].ExternalID != "A" || len(got.Relaxed) != 1 {
		t.Fatalf("request = %#v; факты выдачи должны доехать до ML целиком", got)
	}
	// без notes объяснение не узнает о честном покрытии данных
	if len(got.Notes) != 1 || !strings.Contains(got.Notes[0], "1.9%") {
		t.Fatalf("Notes = %#v; примечания должны доехать до ML", got.Notes)
	}
}

func TestSearchDecodesNotesFromResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"results":[],"explanation":"","parsed":{},`+
			`"data_freshness":"","notes":["ориентация окон известна у 1.9% объявлений"]}`)
	}))
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	resp, err := c.Search(context.Background(), SearchRequest{Query: "окна на юго-запад"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Notes) != 1 {
		t.Fatalf("Notes = %#v; want одну заметку", resp.Notes)
	}
}

func TestExplainStreamStopsWhenConsumerIsGone(t *testing.T) {
	server := explainServer(t, []string{
		tokenFrame("раз"), tokenFrame("два"), doneFrame(true)}, 0, nil)
	defer server.Close()

	consumed := 0
	c := NewMLClient(server.URL, time.Second)
	// Клиент отвалился: onToken вернул false — читать поток дальше незачем.
	if _, err := c.ExplainStream(context.Background(), ExplainRequest{Query: "q"},
		func(string) bool { consumed++; return false }); err != nil {
		t.Fatalf("ExplainStream() error = %v; обрыв потребителя — не ошибка", err)
	}
	if consumed != 1 {
		t.Fatalf("consumed = %d; want 1", consumed)
	}
}

func TestExplainStreamOutlivesUnaryTimeout(t *testing.T) {
	// Таймаут http.Client — общий на весь ответ, поэтому для потока он не
	// годится: объяснение генерируется дольше, чем считается любой unary-вызов.
	// Границу держит контекст, а не клиент.
	server := explainServer(t, []string{
		tokenFrame("медленно"), doneFrame(true)}, 120*time.Millisecond, nil)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var got []string
	c := NewMLClient(server.URL, 50*time.Millisecond)
	llmOK, err := c.ExplainStream(ctx, ExplainRequest{Query: "q"},
		func(tok string) bool { got = append(got, tok); return true })

	if err != nil {
		t.Fatalf("ExplainStream() error = %v", err)
	}
	if !llmOK || strings.Join(got, "") != "медленно" {
		t.Fatalf("tokens = %q, llmOK = %t; поток не должен рваться по unary-таймауту",
			strings.Join(got, ""), llmOK)
	}
}

func TestExplainStreamServerErrorIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.ExplainStream(context.Background(), ExplainRequest{Query: "q"},
		func(string) bool { return true }); !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v; want ErrServer", err)
	}
}

func TestExplainStreamWithoutTerminalFrameIsBadResponse(t *testing.T) {
	// Поток кончился без done — текст мог оборваться на середине, и молча
	// выдавать его за полный ответ нельзя.
	server := explainServer(t, []string{tokenFrame("обрывок")}, 0, nil)
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.ExplainStream(context.Background(), ExplainRequest{Query: "q"},
		func(string) bool { return true }); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("err = %v; want ErrBadResponse", err)
	}
}

func TestSearchAsksMLToSkipTheExplanation(t *testing.T) {
	// Объяснение шлюз забирает потоком: если /search посчитает его сам,
	// выдача объектов задержится на второй вызов LLM (6–17 с) впустую.
	var raw map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		_, _ = fmt.Fprint(w, `{"results":[],"explanation":"","parsed":{},"data_freshness":""}`)
	}))
	defer server.Close()

	c := NewMLClient(server.URL, time.Second)
	if _, err := c.Search(context.Background(),
		SearchRequest{Query: "тихо", Explain: false}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if got, ok := raw["explain"]; !ok || got != false {
		t.Fatalf("explain = %v (present=%t); поле обязано доехать до ML", got, ok)
	}
}
