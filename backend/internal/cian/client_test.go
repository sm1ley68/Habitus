package cian

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	http "github.com/bogdanfinn/fhttp"
)

type queuedDoer struct {
	responses []*http.Response
	requests  []*http.Request
	closed    bool
}

func (doer *queuedDoer) Do(request *http.Request) (*http.Response, error) {
	doer.requests = append(doer.requests, request)
	if len(doer.responses) == 0 {
		return nil, errors.New("unexpected request")
	}
	response := doer.responses[0]
	doer.responses = doer.responses[1:]
	return response, nil
}

func (doer *queuedDoer) CloseIdleConnections() { doer.closed = true }

func response(status int, contentType, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {contentType}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestSessionBootstrapsThenSearches(t *testing.T) {
	t.Parallel()
	doer := &queuedDoer{responses: []*http.Response{
		response(200, "text/html", "<html>Cian home</html>"),
		response(200, "application/json; charset=utf-8", `{"data":{"offersSerialized":[{"cianId":42,"description":"ok"}]}}`),
	}}
	session := newSessionForTest(doer, SessionConfig{
		HomeURL:          "https://www.cian.test/",
		APIURL:           "https://api.cian.test/search",
		Region:           1,
		BootstrapCookies: true,
	})

	offers, err := session.Search(context.Background(), Filter{Room: 2, MinPrice: 10, MaxPrice: 20}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].CianID != "42" {
		t.Fatalf("offers = %#v", offers)
	}
	if len(doer.requests) != 2 || doer.requests[0].Method != http.MethodGet || doer.requests[1].Method != http.MethodPost {
		t.Fatalf("requests = %#v", doer.requests)
	}
	if doer.requests[1].Header["origin"][0] != "https://www.cian.ru" || doer.requests[1].Header["sec-fetch-site"][0] != "same-site" {
		t.Fatalf("API headers = %#v", doer.requests[1].Header)
	}
	requestBody, err := io.ReadAll(doer.requests[1].Body)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		JSONQuery map[string]any `json:"jsonQuery"`
	}
	if err := json.Unmarshal(requestBody, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.JSONQuery["_type"] != "flatsale" {
		t.Fatalf("payload = %s", requestBody)
	}
}

func TestSessionDetectsHTMLCaptchaWithHTTP200(t *testing.T) {
	t.Parallel()
	doer := &queuedDoer{responses: []*http.Response{
		response(200, "text/html; charset=utf-8", "<html><title>Captcha - база объявлений ЦИАН</title></html>"),
	}}
	session := newSessionForTest(doer, SessionConfig{APIURL: "https://api.cian.test/search"})
	_, err := session.Search(context.Background(), Filter{Room: 1}, 1)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("error = %v; want ErrBlocked", err)
	}
}

func TestBuildSearchBodyRejectsInvalidRange(t *testing.T) {
	t.Parallel()
	_, err := BuildSearchBody(1, Filter{Room: 1, MinPrice: 20, MaxPrice: 10}, 1)
	if err == nil {
		t.Fatal("expected error")
	}
}
