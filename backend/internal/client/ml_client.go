// Package client talks to the Python ML service (habitus/online/service.py).
// DTOs mirror habitus/online/schema.py field-for-field — that file is the
// single source of truth for this contract, not the older aspirational
// backend_pipeline_nedvizhimost.md doc.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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
	Results     []ResultItem `json:"results"`
	Explanation string       `json:"explanation"`
	Parsed      ParsedQuery  `json:"parsed"`
	Relaxed     []string     `json:"relaxed"`
	// Notes — честные примечания ML о покрытии данных (например: ориентация
	// окон известна у ~2% объявлений, поэтому учтена как предпочтение, а не
	// как фильтр). Нужны объяснению, иначе оно о них не узнает.
	Notes []string `json:"notes"`
	// Diagnostics — почему выдача пуста: сколько объектов оставалось после
	// каждой клаузы фильтра. ML считает их только при нулевой выдаче.
	Diagnostics   []ConstraintDiagnostic `json:"diagnostics"`
	DataFreshness string                 `json:"data_freshness"`
	Degraded      []string               `json:"degraded"`
	AreaLabel     string                 `json:"area_label"`
	AreaGeojson   any                    `json:"area_geojson"`
	// Intent — намерение реплики многоходового чата (Task 3 ML: TurnIntent).
	// Пустая строка равнозначна отсутствию поля в ответе — до значения по
	// умолчанию его сама ML не заполняет.
	Intent string `json:"intent"`
	// Timings — мс по стадиям пайплайна (Task 1 ML: parse/encode/resolve_area/
	// retrieval/rerank/explain), см. habitus/online/trace.py. Стадия, которая
	// не выполнилась в этом запросе, в словаре отсутствует — нулей вместо
	// отсутствующего замера здесь так же не бывает, как и на стороне ML.
	Timings map[string]float64 `json:"timings"`
}

// ConstraintDiagnostic — один шаг диагностики пустой выдачи
// (habitus/online/retrieval.py::constraint_diagnostics).
type ConstraintDiagnostic struct {
	Constraint string `json:"constraint"`
	Remaining  int    `json:"remaining"`
}

type PointConstraint struct {
	Lon     float64 `json:"lon"`
	Lat     float64 `json:"lat"`
	Minutes int     `json:"minutes"`
	Mode    string  `json:"mode"`
}

type SearchRequest struct {
	Query string           `json:"query"`
	City  string           `json:"city,omitempty"`
	Point *PointConstraint `json:"point,omitempty"`
	// Explain=false — ML не тратит второй вызов LLM внутри /search; текст
	// забирается отдельно через ExplainStream. Без omitempty: false здесь
	// значимое значение, а не «поле не задано».
	Explain bool `json:"explain"`
	// PrevParsed — разбор предыдущего шага диалога (chat_searches.parsed_query
	// на стороне шлюза), контекст для multi-turn чата (Task 3/4). nil для
	// первого поиска в чате — omitempty делает поле неотличимым от null для ML.
	PrevParsed map[string]any `json:"prev_parsed,omitempty"`
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

type OwnerUpsertRequest struct {
	ExternalID        string   `json:"external_id"`
	Source            string   `json:"source"`
	City              string   `json:"city"`
	Price             *int64   `json:"price"`
	Area              *float32 `json:"area"`
	KitchenArea       *float32 `json:"kitchen_area"`
	Rooms             *int     `json:"rooms"`
	Level             *int     `json:"level"`
	Levels            *int     `json:"levels"`
	Address           string   `json:"address"`
	Lng               float64  `json:"lng"`
	Lat               float64  `json:"lat"`
	WindowOrientation []string `json:"window_orientation"`
	Description       string   `json:"description"`
	Photos            []string `json:"photos"`
	SourceURL         string   `json:"source_url"`
}

type OwnerUpsertResponse struct {
	ExternalID string `json:"external_id"`
	Indexed    bool   `json:"indexed"`
}

type OwnerWithdrawResponse struct {
	ExternalID  string `json:"external_id"`
	Deactivated bool   `json:"deactivated"`
}

// OwnerListingInvalidError — 422 от ML: объявление не прошло пороги витрины.
// Отдельный тип, а не ErrBadResponse: продавцу нужно показать, какое поле
// поправить, и без имени поля сообщение бесполезно.
type OwnerListingInvalidError struct {
	Field   string
	Message string
}

func (e *OwnerListingInvalidError) Error() string {
	return e.Field + ": " + e.Message
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

type ExplainRequest struct {
	Query   string       `json:"query"`
	Results []ResultItem `json:"results"`
	Relaxed []string     `json:"relaxed"`
	Notes   []string     `json:"notes,omitempty"`
}

type MLClient struct {
	baseURL string
	http    *http.Client
	// stream carries SSE responses. Deliberately without http.Client.Timeout:
	// that deadline covers the whole response body, so it would cut a live
	// stream mid-generation. Streaming deadlines come from the context.
	stream *http.Client
}

func NewMLClient(baseURL string, timeout time.Duration) *MLClient {
	return &MLClient{
		baseURL: baseURL,
		http:    &http.Client{Timeout: timeout},
		stream:  &http.Client{},
	}
}

func (c *MLClient) Search(ctx context.Context, req SearchRequest) (*SearchResponse, error) {
	const endpoint = "/search"
	started := time.Now()

	body, err := json.Marshal(req)
	if err != nil {
		return nil, transportFailure(ErrBadResponse, endpoint,
			fmt.Errorf("encode request: %w", err), started)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, transportFailure(kindOfTransportError(ctx, err), endpoint, err, started)
	}
	defer resp.Body.Close()

	if kind := kindOfStatus(resp.StatusCode); kind != nil {
		return nil, httpFailure(kind, endpoint, resp, started)
	}

	var out SearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, transportFailure(ErrBadResponse, endpoint,
			fmt.Errorf("decode: %w", err), started)
	}
	return &out, nil
}

func (c *MLClient) postJSON(ctx context.Context, path string, in, out any) error {
	return c.post(ctx, path, in, out, false)
}

// postJSONWithValidation отличается от postJSON одним: 422 разбирается в
// OwnerListingInvalidError вместо того, чтобы схлопнуться в ErrBadResponse.
func (c *MLClient) postJSONWithValidation(ctx context.Context, path string, in, out any) error {
	return c.post(ctx, path, in, out, true)
}

func (c *MLClient) post(ctx context.Context, path string, in, out any, parseValidation bool) error {
	started := time.Now()

	body, err := json.Marshal(in)
	if err != nil {
		return transportFailure(ErrBadResponse, path,
			fmt.Errorf("encode request: %w", err), started)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return transportFailure(kindOfTransportError(ctx, err), path, err, started)
	}
	defer resp.Body.Close()
	if parseValidation && resp.StatusCode == http.StatusUnprocessableEntity {
		var detail struct {
			Detail struct {
				Field   string `json:"field"`
				Message string `json:"message"`
			} `json:"detail"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
			return transportFailure(ErrBadResponse, path,
				fmt.Errorf("decode 422: %w", err), started)
		}
		return &OwnerListingInvalidError{Field: detail.Detail.Field, Message: detail.Detail.Message}
	}
	if kind := kindOfStatus(resp.StatusCode); kind != nil {
		return httpFailure(kind, path, resp, started)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return transportFailure(ErrBadResponse, path,
			fmt.Errorf("decode: %w", err), started)
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

func (c *MLClient) OwnerUpsert(ctx context.Context, req OwnerUpsertRequest) (*OwnerUpsertResponse, error) {
	var out OwnerUpsertResponse
	if err := c.postJSONWithValidation(ctx, "/listings/owner-upsert", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *MLClient) OwnerWithdraw(ctx context.Context, externalID string) (*OwnerWithdrawResponse, error) {
	var out OwnerWithdrawResponse
	if err := c.postJSON(ctx, "/listings/owner-withdraw",
		map[string]string{"external_id": externalID}, &out); err != nil {
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

// ExplainStream reads the ML explanation stream, handing each token to onToken
// as it arrives, and returns llm_ok from the terminal `done` frame. onToken
// returning false means the consumer is gone: the read stops, which is not an
// error. A stream that ends without `done` is — the text may be truncated, and
// passing it off as a complete answer would be a lie.
func (c *MLClient) ExplainStream(ctx context.Context, req ExplainRequest,
	onToken func(string) bool) (bool, error) {
	const endpoint = "/explain/stream"
	started := time.Now()

	body, err := json.Marshal(req)
	if err != nil {
		return false, transportFailure(ErrBadResponse, endpoint,
			fmt.Errorf("encode request: %w", err), started)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.stream.Do(httpReq)
	if err != nil {
		return false, transportFailure(kindOfTransportError(ctx, err), endpoint, err, started)
	}
	defer resp.Body.Close()

	if kind := kindOfStatus(resp.StatusCode); kind != nil {
		return false, httpFailure(kind, endpoint, resp, started)
	}

	scanner := bufio.NewScanner(resp.Body)
	var event, data string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "": // blank line terminates the frame
			switch event {
			case "token":
				var frame struct {
					Token string `json:"token"`
				}
				if err := json.Unmarshal([]byte(data), &frame); err != nil {
					return false, transportFailure(ErrBadResponse, endpoint,
						fmt.Errorf("decode token: %w", err), started)
				}
				if !onToken(frame.Token) {
					return false, nil
				}
			case "done":
				var frame struct {
					LLMOK bool `json:"llm_ok"`
				}
				if err := json.Unmarshal([]byte(data), &frame); err != nil {
					return false, transportFailure(ErrBadResponse, endpoint,
						fmt.Errorf("decode done: %w", err), started)
				}
				return frame.LLMOK, nil
			}
			event, data = "", ""
		}
	}
	if err := scanner.Err(); err != nil {
		return false, transportFailure(kindOfTransportError(ctx, err), endpoint, err, started)
	}
	return false, transportFailure(ErrBadResponse, endpoint,
		errors.New("поток объяснения оборвался без кадра done"), started)
}

// WarmUp fires a throwaway search so the ML process's lazily-loaded models
// (BGE-M3, reranker) load once at container start rather than on the first
// real user request. The caller decides whether a failed warm-up is fatal.
func (c *MLClient) WarmUp(ctx context.Context) error {
	_, err := c.Search(ctx, SearchRequest{Query: "квартира"})
	return err
}
