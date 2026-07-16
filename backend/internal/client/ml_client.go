// Package client talks to the Python ML service (habitus/online/service.py).
// DTOs mirror habitus/online/schema.py field-for-field — that file is the
// single source of truth for this contract, not the older aspirational
// backend_pipeline_nedvizhimost.md doc.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	ErrTimeout     = errors.New("ml: timeout")
	ErrUnavailable = errors.New("ml: service unavailable")
	ErrServer      = errors.New("ml: server error")
	ErrBadResponse = errors.New("ml: bad response")
)

type GeoConstraint struct {
	Kind        string `json:"kind"`
	WalkMinutes int    `json:"walk_minutes"`
}

type HouseholdLegIntent struct {
	ToLabel string  `json:"to_label"`
	ToKind  string  `json:"to_kind"`
	Mode    string  `json:"mode"`
	Depart  *string `json:"depart"`
	Arrive  *string `json:"arrive"`
}

type HouseholdMemberIntent struct {
	ID    string               `json:"id"`
	Label string               `json:"label"`
	Legs  []HouseholdLegIntent `json:"legs"`
}

type ParsedQuery struct {
	PriceMin          *int64                  `json:"price_min"`
	PriceMax          *int64                  `json:"price_max"`
	Rooms             []int                   `json:"rooms"`
	AreaMin           *float64                `json:"area_min"`
	AreaMax           *float64                `json:"area_max"`
	Geo               []GeoConstraint         `json:"geo"`
	WindowOrientation []string                `json:"window_orientation"`
	NoiseMax          *string                 `json:"noise_max"`
	StopFactors       []string                `json:"stop_factors"`
	SemanticText      string                  `json:"semantic_text"`
	Lang              string                  `json:"lang"`
	Household         []HouseholdMemberIntent `json:"household"`
}

type ResultItem struct {
	ExternalID   string         `json:"external_id"`
	Price        *int64         `json:"price"`
	Area         *float64       `json:"area"`
	Rooms        *int           `json:"rooms"`
	AddressFacts map[string]any `json:"address_facts"`
	Score        float64        `json:"score"`
}

type SearchResponse struct {
	Results       []ResultItem `json:"results"`
	Explanation   string       `json:"explanation"`
	Parsed        ParsedQuery  `json:"parsed"`
	Relaxed       []string     `json:"relaxed"`
	DataFreshness string       `json:"data_freshness"`
	Degraded      []string     `json:"degraded"`
}

type PointConstraint struct {
	Lon     float64 `json:"lon"`
	Lat     float64 `json:"lat"`
	Minutes int     `json:"minutes"`
	Mode    string  `json:"mode"`
}

type SearchRequest struct {
	Query string           `json:"query"`
	Point *PointConstraint `json:"point,omitempty"`
}

type DossierRequest struct {
	ObjectID    string         `json:"object_id"`
	City        string         `json:"city"`
	RawQuery    string         `json:"raw_query"`
	ParsedQuery map[string]any `json:"parsed_query"`
	Relaxed     []string       `json:"relaxed"`
	Degraded    []string       `json:"degraded"`
}

type DossierResponse struct {
	Dossier       map[string]any `json:"dossier"`
	SchemaVersion string         `json:"schema_version"`
}

type ObjectAskRequest struct {
	Question      string         `json:"question"`
	Passport      map[string]any `json:"passport"`
	SearchContext map[string]any `json:"search_context"`
}

type GroundedSentence struct {
	Text          string   `json:"text"`
	EvidencePaths []string `json:"evidence_paths"`
	Unknown       bool     `json:"unknown"`
}

type ObjectAskResponse struct {
	Sentences []GroundedSentence `json:"sentences"`
}

type MLClient struct {
	baseURL string
	http    *http.Client
}

func NewMLClient(baseURL string, timeout time.Duration) *MLClient {
	return &MLClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *MLClient) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request: %v", ErrBadResponse, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return nil, ErrServer
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: status %d", ErrBadResponse, resp.StatusCode)
	}

	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrBadResponse, err)
	}
	return &out, nil
}

func (c *MLClient) postJSON(ctx context.Context, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("%w: encode request: %v", ErrBadResponse, err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrTimeout
		}
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return ErrServer
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d", ErrBadResponse, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrBadResponse, err)
	}
	return nil
}

func (c *MLClient) Dossier(ctx context.Context, req DossierRequest) (*DossierResponse, error) {
	var out DossierResponse
	if err := c.postJSON(ctx, "/dossier", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MLClient) AskObject(ctx context.Context, req ObjectAskRequest) (*ObjectAskResponse, error) {
	var out ObjectAskResponse
	if err := c.postJSON(ctx, "/object-ask", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WarmUp fires a throwaway search so the ML process's lazily-loaded models
// (BGE-M3, reranker) load once at container start rather than on the first
// real user request. The caller decides whether a failed warm-up is fatal.
func (c *MLClient) WarmUp(ctx context.Context) error {
	_, err := c.Search(ctx, SearchRequest{Query: "квартира"})
	return err
}
