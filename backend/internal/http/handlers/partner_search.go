// partner_search.go — поиск, выдача, объект, досье и вопрос по объекту в
// Partner API. Синхронные JSON-ручки вместо SSE: у интеграции нет
// пользователя, которому нужно показывать стадии.
package handlers

import (
	"strings"
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type PartnerSearchHandler struct {
	searches *service.PartnerSearchService
}

func NewPartnerSearchHandler(searches *service.PartnerSearchService) *PartnerSearchHandler {
	return &PartnerSearchHandler{searches: searches}
}

type partnerSearchBody struct {
	Query        string              `json:"query"`
	City         string              `json:"city"`
	Point        *streamPointRequest `json:"point"`
	Explain      *bool               `json:"explain"`
	Limit        int                 `json:"limit"`
	PrevSearchID string              `json:"prev_search_id"`
}

// Create implements POST /partner/v1/search.
func (h *PartnerSearchHandler) Create(c *fiber.Ctx) error {
	var body partnerSearchBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	query := strings.TrimSpace(body.Query)
	if query == "" {
		return apperr.Validation("query обязателен").WithParam("query")
	}
	if utf8.RuneCountInString(query) > 2000 {
		return apperr.Validation("query длиннее 2000 символов").WithParam("query")
	}
	point, err := normalizePoint(body.Point)
	if err != nil {
		return apperr.Validation(err.Error()).WithParam("point")
	}
	in := service.PartnerSearchInput{
		Query: query, City: strings.TrimSpace(body.City), Point: point, Limit: body.Limit,
	}
	if body.Explain != nil {
		in.Explain = *body.Explain
	}
	if raw := strings.TrimSpace(body.PrevSearchID); raw != "" {
		id, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			return apperr.Validation("prev_search_id не похож на идентификатор поиска").
				WithParam("prev_search_id")
		}
		in.PrevSearchID = &id
	}

	out, err := h.searches.Search(c.Context(), middleware.PartnerIdentity(c).Partner, in)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(partnerSearchDTO(out))
}

// Get implements GET /partner/v1/searches/{search_id}.
func (h *PartnerSearchHandler) Get(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "search_id", apperr.SearchNotFound)
	if err != nil {
		return err
	}
	search, err := h.searches.GetSearch(c.Context(), middleware.PartnerIdentity(c).Partner.ID, id)
	if err != nil {
		return err
	}
	return c.JSON(searchDTO(search))
}

// Results implements GET /partner/v1/searches/{search_id}/results.
func (h *PartnerSearchHandler) Results(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "search_id", apperr.SearchNotFound)
	if err != nil {
		return err
	}
	page, err := ParsePage(c)
	if err != nil {
		return err
	}
	objects, total, consumed, err := h.searches.Results(c.Context(),
		middleware.PartnerIdentity(c).Partner.ID, id, page.Limit, page.Offset)
	if err != nil {
		return err
	}
	data := make([]fiber.Map, 0, len(objects))
	for _, o := range objects {
		data = append(data, resultObjectDTO(o))
	}
	return c.JSON(ListResponse(ObjectSearchResult, data, page, total, consumed))
}

// Object implements GET /partner/v1/properties/{object_id} — факты объекта
// вне подбора. Процента совпадения и досье здесь нет и быть не может: оба
// привязаны к запросу, и без запроса это были бы выдуманные числа.
func (h *PartnerSearchHandler) Object(c *fiber.Ctx) error {
	passport, err := h.searches.Object(c.Context(), c.Params("object_id"))
	if err != nil {
		return err
	}
	return c.JSON(passportDTO(passport))
}

// Dossier implements GET /partner/v1/searches/{search_id}/properties/{object_id}
// — тот же объект, но в контексте запроса: с процентом совпадения, вердиктом
// и блоками досье.
func (h *PartnerSearchHandler) Dossier(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "search_id", apperr.SearchNotFound)
	if err != nil {
		return err
	}
	passport, err := h.searches.Passport(c.Context(),
		middleware.PartnerIdentity(c).Partner.ID, id, c.Params("object_id"))
	if err != nil {
		return err
	}
	return c.JSON(passportDTO(passport))
}

type partnerAskBody struct {
	Question string `json:"question"`
}

// Ask implements POST /partner/v1/searches/{search_id}/properties/{object_id}/ask.
func (h *PartnerSearchHandler) Ask(c *fiber.Ctx) error {
	id, err := partnerUUID(c, "search_id", apperr.SearchNotFound)
	if err != nil {
		return err
	}
	var body partnerAskBody
	if err := c.BodyParser(&body); err != nil {
		return apperr.Validation("Не удалось разобрать тело запроса")
	}
	question := strings.TrimSpace(body.Question)
	if question == "" {
		return apperr.Validation("question обязателен").WithParam("question")
	}
	if utf8.RuneCountInString(question) > 2000 {
		return apperr.Validation("question длиннее 2000 символов").WithParam("question")
	}
	answer, err := h.searches.Ask(c.Context(), middleware.PartnerIdentity(c).Partner.ID,
		id, c.Params("object_id"), question)
	if err != nil {
		return err
	}
	return c.JSON(answerDTO(answer))
}

// --- DTO ---

func partnerSearchDTO(out service.PartnerSearchOutput) fiber.Map {
	data := make([]fiber.Map, 0, len(out.Objects))
	for _, o := range out.Objects {
		data = append(data, resultObjectDTO(o))
	}
	dto := searchDTO(out.Search)
	dto["results"] = fiber.Map{
		"object":      ObjectList,
		"item_object": ObjectSearchResult,
		"data":        data,
		"total":       out.Search.Total,
		"has_more":    out.HasMore,
		"next_cursor": nextCursorFor(len(data), out.HasMore),
	}
	return dto
}

func nextCursorFor(shown int, hasMore bool) any {
	if !hasMore {
		return nil
	}
	return encodeCursor(shown)
}

func searchDTO(s domain.PartnerSearch) fiber.Map {
	return fiber.Map{
		"object":      ObjectSearch,
		"id":          s.ID,
		"query":       s.Query,
		"city":        s.City,
		"created_at":  s.CreatedAt,
		"parsed":      nonNilAnyMap(s.ParsedQuery),
		"relaxed":     ownerStrings(s.Relaxed),
		"degraded":    ownerStrings(s.Degraded),
		"notes":       ownerStrings(s.Notes),
		"explanation": s.Explanation,
		// Пустая строка означала бы «свежесть данных известна и она пустая».
		// Здесь честнее null: замера нет.
		"data_freshness": nullableString(s.DataFreshness),
		"area_label":     nullableString(s.AreaLabel),
		"area_geojson":   s.AreaGeoJSON,
		"total":          s.Total,
	}
}

func resultObjectDTO(o service.FinalResultObject) fiber.Map {
	return fiber.Map{
		"object":      ObjectSearchResult,
		"id":          o.ID,
		"name":        o.Name,
		"address":     o.Address,
		"cover_image": o.CoverImage,
		"match_score": o.MatchScore,
		"coordinates": o.Coordinates,
		"price":       o.PriceFrom,
		"rooms":       o.Rooms,
		"area_sqm":    o.AreaSqm,
		"floor":       nullableString(o.Floor),
		"tags":        ownerStrings(o.Tags),
	}
}

func passportDTO(p service.ObjectPassport) fiber.Map {
	return fiber.Map{
		"object":      ObjectProperty,
		"id":          p.ID,
		"name":        p.Name,
		"address":     p.Address,
		"price":       p.Price,
		"rooms":       p.Rooms,
		"area_sqm":    p.AreaSqm,
		"floor":       nullableString(p.Floor),
		"images":      ownerStrings(p.Images),
		"coordinates": p.Coordinates,
		"contact":     p.Contact,
		"analysis":    p.LifestyleAnalysis,
	}
}

func answerDTO(a service.PartnerAnswer) fiber.Map {
	sentences := make([]fiber.Map, 0, len(a.Sentences))
	for _, s := range a.Sentences {
		sentences = append(sentences, fiber.Map{
			"text": s.Text, "evidence_paths": evidencePaths(s.EvidencePaths),
			// unknown=true — модель честно сказала «в досье этого нет».
			// Отдельным полем, а не фразой в тексте: интеграции нужно уметь
			// отличить ответ от признания незнания программно.
			"unknown": s.Unknown,
		})
	}
	return fiber.Map{"object": ObjectAnswer, "answer": a.Answer, "sentences": sentences}
}

func evidencePaths(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// nullableString отдаёт null вместо пустой строки: в этом проекте пустое
// значение и отсутствующее — разные вещи, и стирать разницу нельзя.
func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
