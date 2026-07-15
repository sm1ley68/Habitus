package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
