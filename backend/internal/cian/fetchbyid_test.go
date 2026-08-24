package cian

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

func TestBuildOfferBodyFiltersByID(t *testing.T) {
	t.Parallel()
	body, err := BuildOfferBody(1, 318394906)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var decoded struct {
		JSONQuery struct {
			Type string `json:"_type"`
			IDs  struct {
				Type  string  `json:"type"`
				Value []int64 `json:"value"`
			} `json:"ids"`
			Region struct {
				Value []int `json:"value"`
			} `json:"region"`
		} `json:"jsonQuery"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("тело запроса не разбирается: %v", err)
	}
	if decoded.JSONQuery.Type != "flatsale" {
		t.Fatalf("_type = %q", decoded.JSONQuery.Type)
	}
	if decoded.JSONQuery.IDs.Type != "terms" || len(decoded.JSONQuery.IDs.Value) != 1 ||
		decoded.JSONQuery.IDs.Value[0] != 318394906 {
		t.Fatalf("фильтр ids собран неверно: %+v", decoded.JSONQuery.IDs)
	}
	if len(decoded.JSONQuery.Region.Value) != 1 || decoded.JSONQuery.Region.Value[0] != 1 {
		t.Fatalf("регион собран неверно: %+v", decoded.JSONQuery.Region)
	}
}

func TestBuildOfferBodyRejectsBadInput(t *testing.T) {
	t.Parallel()
	if _, err := BuildOfferBody(1, 0); err == nil {
		t.Fatal("нулевой id должен отвергаться")
	}
	if _, err := BuildOfferBody(0, 123); err == nil {
		t.Fatal("нулевой регион должен отвергаться")
	}
}

func TestFetchByIDParsesOffer(t *testing.T) {
	t.Parallel()
	doer := &queuedDoer{responses: []*http.Response{
		response(200, "application/json; charset=utf-8", offerResponseJSON),
	}}
	session := newSessionForTest(doer, SessionConfig{APIURL: "https://api.cian.test/search"})

	listing, err := session.FetchByID(context.Background(), 318394906)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if listing.CianID != "318394906" {
		t.Fatalf("cian_id = %q", listing.CianID)
	}
	if listing.Price == nil || *listing.Price != 45007350 {
		t.Fatalf("price = %v", listing.Price)
	}
	if listing.Latitude == nil || listing.Longitude == nil {
		t.Fatal("координаты обязаны быть разобраны")
	}
	if listing.CollectedAt.IsZero() {
		t.Fatal("collected_at должен быть проставлен")
	}
	if time.Since(listing.CollectedAt) > time.Minute {
		t.Fatal("collected_at должен быть моментом запроса")
	}
}

func TestFetchByIDNotFoundOnEmptyList(t *testing.T) {
	t.Parallel()
	doer := &queuedDoer{responses: []*http.Response{
		response(200, "application/json; charset=utf-8", `{"data":{"offersSerialized":[]}}`),
	}}
	session := newSessionForTest(doer, SessionConfig{APIURL: "https://api.cian.test/search"})

	if _, err := session.FetchByID(context.Background(), 1); !errors.Is(err, ErrOfferNotFound) {
		t.Fatalf("ожидался ErrOfferNotFound, получено %v", err)
	}
}

func TestFetchByIDReportsBlock(t *testing.T) {
	t.Parallel()
	doer := &queuedDoer{responses: []*http.Response{
		response(200, "text/html; charset=utf-8", "<html><title>Captcha - база объявлений ЦИАН</title></html>"),
	}}
	session := newSessionForTest(doer, SessionConfig{APIURL: "https://api.cian.test/search"})

	if _, err := session.FetchByID(context.Background(), 1); !errors.Is(err, ErrBlocked) {
		t.Fatalf("ожидался ErrBlocked, получено %v", err)
	}
}
