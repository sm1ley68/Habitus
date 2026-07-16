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
