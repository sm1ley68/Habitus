package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// До MLFailure тело неуспешного ответа ML выбрасывалось: 5xx схлопывался в
// голый ErrServer, 4xx — в строку «status 404». Причина, которую ML честно
// написала в detail (какая стадия, что за исключение, что чинить), терялась
// на границе двух сервисов — ровно там, где она была нужна.

func TestServerErrorCarriesTheStructuredDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"detail":{"code":"db_unavailable",
			"stage":"retrieval","message":"Нет связи с базой: connection refused",
			"hint":"Postgres по адресу db:5432/habitus не отвечает",
			"timings":{"parse":812.5}}}`))
	}))
	defer server.Close()

	_, err := NewMLClient(server.URL, time.Second).Search(t.Context(), SearchRequest{Query: "тихо"})

	var failure *MLFailure
	if !errors.As(err, &failure) {
		t.Fatalf("ожидался *MLFailure, получено %T (%v)", err, err)
	}
	if !errors.Is(err, ErrServer) {
		t.Fatal("errors.Is(err, ErrServer) обязан продолжать работать — на нём стоит вся маршрутизация отказов")
	}
	if failure.Code != "db_unavailable" || failure.Stage != "retrieval" {
		t.Fatalf("code=%q stage=%q — диагноз ML не доехал", failure.Code, failure.Stage)
	}
	if !strings.Contains(failure.Detail, "connection refused") {
		t.Fatalf("причина = %q", failure.Detail)
	}
	if failure.Hint == "" {
		t.Fatal("подсказка «что чинить» потерялась")
	}
	if failure.Timings["parse"] != 812.5 {
		t.Fatalf("тайминги успевших стадий = %v", failure.Timings)
	}
	if failure.Status != 503 || failure.Endpoint != "/search" {
		t.Fatalf("status=%d endpoint=%q", failure.Status, failure.Endpoint)
	}
}

func TestBadResponseKeepsStatusAndBody(t *testing.T) {
	// ML_SERVICE_URL смотрит на чужой процесс: 404 с HTML-страницей вместо JSON.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("<html><body>404 Not Found</body></html>"))
	}))
	defer server.Close()

	_, err := NewMLClient(server.URL, time.Second).Search(t.Context(), SearchRequest{Query: "тихо"})

	var failure *MLFailure
	if !errors.As(err, &failure) {
		t.Fatalf("ожидался *MLFailure, получено %T", err)
	}
	if !errors.Is(err, ErrBadResponse) {
		t.Fatal("404 — это ErrBadResponse")
	}
	if failure.Status != 404 {
		t.Fatalf("статус = %d", failure.Status)
	}
	if !strings.Contains(failure.Detail, "404 Not Found") {
		t.Fatalf("тело ответа — главная улика, а его нет: %q", failure.Detail)
	}
}

func TestPlainStringDetailIsKeptAsIs(t *testing.T) {
	// HTTPException(status_code=404, detail="object not found") — detail строкой.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"detail":"object not found"}`))
	}))
	defer server.Close()

	_, err := NewMLClient(server.URL, time.Second).Search(t.Context(), SearchRequest{Query: "тихо"})

	var failure *MLFailure
	errors.As(err, &failure)
	if failure.Detail != "object not found" {
		t.Fatalf("detail = %q", failure.Detail)
	}
	if failure.Code != "" {
		t.Fatalf("кода ML не присылала — выдумывать его нельзя, получено %q", failure.Code)
	}
}

func TestUnreachableServiceNamesTheEndpoint(t *testing.T) {
	// Порт заведомо никем не занят: отказ на уровне соединения.
	_, err := NewMLClient("http://127.0.0.1:1", time.Second).Search(t.Context(), SearchRequest{Query: "тихо"})

	var failure *MLFailure
	if !errors.As(err, &failure) {
		t.Fatalf("ожидался *MLFailure, получено %T", err)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("соединение не установилось — это ErrUnavailable")
	}
	if failure.Endpoint != "/search" {
		t.Fatalf("endpoint = %q — без него непонятно, куда именно не достучались", failure.Endpoint)
	}
	if failure.Detail == "" {
		t.Fatal("текст сетевой ошибки обязан доезжать: в нём хост и порт")
	}
}

func TestFailureErrorTextNamesEndpointAndStatus(t *testing.T) {
	failure := &MLFailure{
		Kind: ErrServer, Endpoint: "/search", Status: 500,
		Code: "db_schema_missing", Stage: "retrieval",
		Detail: "relation \"listings\" does not exist",
	}

	text := failure.Error()
	for _, want := range []string{"/search", "500", "db_schema_missing", "retrieval", "listings"} {
		if !strings.Contains(text, want) {
			t.Fatalf("в тексте ошибки нет %q: %s", want, text)
		}
	}
}

func TestTimeoutFailureRecordsHowLongItWaited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Отвечаем позже, чем истекает бюджет вызова. Верхняя граница
		// обязательна: отмена запроса на стороне клиента гасит серверный
		// контекст не всегда, а Close() ждёт завершения обработчика.
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Millisecond)
	defer cancel()
	_, err := NewMLClient(server.URL, time.Minute).Search(ctx, SearchRequest{Query: "тихо"})

	var failure *MLFailure
	if !errors.As(err, &failure) {
		t.Fatalf("ожидался *MLFailure, получено %T (%v)", err, err)
	}
	if !errors.Is(err, ErrTimeout) {
		t.Fatal("дедлайн контекста — это ErrTimeout")
	}
	if failure.Elapsed < 100*time.Millisecond {
		t.Fatalf("сколько реально прождали = %v", failure.Elapsed)
	}
}
