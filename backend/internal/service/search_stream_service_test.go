package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
