package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type ResultsHandler struct {
	results *service.ResultsService
}

func NewResultsHandler(results *service.ResultsService) *ResultsHandler {
	return &ResultsHandler{results: results}
}

const (
	resultsDefaultLimit = 10
	resultsMaxLimit     = 50
)

// parseResultsPage разбирает offset/limit «показать ещё» (Task 7) — дефолты и
// нижняя граница как у parseLimitOffset (используемого остальными ручками
// чата), плюс верхний потолок на limit. Кривое значение параметра тихо
// заменяется дефолтом, как parseBbox в geo_handler.go: 400 из-за этого не
// отдаём.
func parseResultsPage(c *fiber.Ctx) (limit, offset int) {
	limit, offset = parseLimitOffset(c, resultsDefaultLimit)
	if limit > resultsMaxLimit {
		limit = resultsMaxLimit
	}
	return limit, offset
}

// List implements GET /chats/{chat_id}/results?offset=&limit= — постраничный
// доступ к уже сохранённому пулу последнего поиска чата («показать ещё»,
// Task 7): весь набор из ответа ML уже лежит в chat_search_results, поэтому
// запрос сюда не бьёт повторно в ML.
func (h *ResultsHandler) List(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	limit, offset := parseResultsPage(c)

	objects, count, total, err := h.results.List(c.Context(), middleware.UserID(c), chatID, limit, offset)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"objects": objects, "count": count, "total": total})
}
