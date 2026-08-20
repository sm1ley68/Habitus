package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

// parseResultsPageFromQuery прогоняет разбор через реальный fiber.Ctx с
// заданной query-строкой — так же, как разбор bbox в geo_handler.go
// проверяется через фактический запрос, а не подставной интерфейс.
func parseResultsPageFromQuery(t *testing.T, rawQuery string) (limit, offset int) {
	t.Helper()
	app := fiber.New()
	var gotLimit, gotOffset int
	app.Get("/x", func(c *fiber.Ctx) error {
		gotLimit, gotOffset = parseResultsPage(c)
		return c.SendStatus(fiber.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/x"+rawQuery, nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	defer resp.Body.Close()
	return gotLimit, gotOffset
}

func TestParseResultsPageDefaults(t *testing.T) {
	limit, offset := parseResultsPageFromQuery(t, "")
	if limit != resultsDefaultLimit || offset != 0 {
		t.Fatalf("limit/offset = %d/%d; want %d/0", limit, offset, resultsDefaultLimit)
	}
}

func TestParseResultsPageClampsLimitAbove50(t *testing.T) {
	limit, _ := parseResultsPageFromQuery(t, "?limit=500")
	if limit != resultsMaxLimit {
		t.Fatalf("limit = %d; want %d (обрезано до максимума)", limit, resultsMaxLimit)
	}
}

func TestParseResultsPageNegativeOffsetFallsBackToZero(t *testing.T) {
	_, offset := parseResultsPageFromQuery(t, "?offset=-5")
	if offset != 0 {
		t.Fatalf("offset = %d; want 0 (отрицательный офсет — дефолт)", offset)
	}
}

func TestParseResultsPageIgnoresGarbageSilently(t *testing.T) {
	// Кривое значение параметра — тихий дефолт, не ошибка (как parseBbox).
	limit, offset := parseResultsPageFromQuery(t, "?limit=abc&offset=xyz")
	if limit != resultsDefaultLimit || offset != 0 {
		t.Fatalf("limit/offset = %d/%d; want дефолты %d/0", limit, offset, resultsDefaultLimit)
	}
}

func TestParseResultsPageWithinBoundsPassesThrough(t *testing.T) {
	limit, offset := parseResultsPageFromQuery(t, "?limit=25&offset=30")
	if limit != 25 || offset != 30 {
		t.Fatalf("limit/offset = %d/%d; want 25/30", limit, offset)
	}
}
