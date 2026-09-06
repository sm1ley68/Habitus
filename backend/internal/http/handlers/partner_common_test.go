package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func pageFromQuery(t *testing.T, rawQuery string) (Page, error) {
	t.Helper()
	app := fiber.New()
	var got Page
	var gotErr error
	app.Get("/x", func(c *fiber.Ctx) error {
		got, gotErr = ParsePage(c)
		return c.SendStatus(fiber.StatusOK)
	})
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/x"+rawQuery, nil))
	if err != nil {
		t.Fatalf("app.Test() error = %v", err)
	}
	_ = resp.Body.Close()
	return got, gotErr
}

func TestParsePageDefaultsAndClamp(t *testing.T) {
	page, err := pageFromQuery(t, "")
	if err != nil || page.Limit != partnerDefaultLimit || page.Offset != 0 {
		t.Fatalf("page = %+v, err = %v; ожидались дефолты", page, err)
	}
	page, err = pageFromQuery(t, "?limit=5000")
	if err != nil || page.Limit != partnerMaxLimit {
		t.Fatalf("limit = %d; должен обрезаться до %d", page.Limit, partnerMaxLimit)
	}
}

// В отличие от карты во фронте, здесь кривой параметр — ошибка, а не тихий
// дефолт: молча потерянные данные интеграция замечает поздно и дорого.
func TestParsePageRejectsGarbageLimit(t *testing.T) {
	for _, q := range []string{"?limit=abc", "?limit=0", "?limit=-3"} {
		if _, err := pageFromQuery(t, q); err == nil {
			t.Errorf("ParsePage(%q) принял негодный limit", q)
		}
	}
}

func TestCursorRoundTrips(t *testing.T) {
	cursor := encodeCursor(40)
	page, err := pageFromQuery(t, "?cursor="+cursor)
	if err != nil {
		t.Fatalf("ParsePage() error = %v", err)
	}
	if page.Offset != 40 {
		t.Fatalf("offset = %d; want 40", page.Offset)
	}
}

// Молчаливый сброс на первую страницу закольцевал бы клиента на ней навсегда,
// и заметить это он смог бы только по счётчику.
func TestParsePageRejectsBrokenCursor(t *testing.T) {
	for _, q := range []string{"?cursor=!!!", "?cursor=" + encodeCursorRaw("x:5"), "?cursor=" + encodeCursorRaw("o:-1")} {
		if _, err := pageFromQuery(t, q); err == nil {
			t.Errorf("ParsePage(%q) принял негодный курсор", q)
		}
	}
}

func encodeCursorRaw(value string) string {
	return base64RawURL(value)
}

func TestListResponseStopsAtLastPage(t *testing.T) {
	data := []fiber.Map{{"id": 1}, {"id": 2}}

	middle := ListResponse(ObjectLead, data, Page{Limit: 2, Offset: 0}, 5, 2)
	if middle["has_more"] != true || middle["next_cursor"] == nil {
		t.Fatalf("середина списка: has_more = %v, next_cursor = %v", middle["has_more"], middle["next_cursor"])
	}

	last := ListResponse(ObjectLead, data, Page{Limit: 2, Offset: 3}, 5, 2)
	if last["has_more"] != false {
		t.Fatalf("has_more = %v; на последней странице должно быть false", last["has_more"])
	}
	// null, а не пустая строка: клиент листает, пока курсор не станет null.
	if last["next_cursor"] != nil {
		t.Fatalf("next_cursor = %v; на последней странице должен быть null", last["next_cursor"])
	}
	if last["item_object"] != ObjectLead || last["object"] != ObjectList {
		t.Fatalf("конверт списка собран неверно: %v", last)
	}
}

// Объект, пропавший из витрины после подбора, не попадает в выдачу, но
// страницу он занял: курсор обязан сдвинуться на прочитанное, иначе
// следующая страница повторит уже отданные объекты.
func TestListResponseAdvancesCursorByRowsRead(t *testing.T) {
	shown := []fiber.Map{{"id": 1}, {"id": 2}}

	page := ListResponse(ObjectSearchResult, shown, Page{Limit: 3, Offset: 0}, 9, 3)

	cursor, ok := page["next_cursor"].(string)
	if !ok {
		t.Fatalf("next_cursor = %v; ожидалась строка", page["next_cursor"])
	}
	offset, err := decodeCursor(cursor)
	if err != nil {
		t.Fatalf("decodeCursor() error = %v", err)
	}
	if offset != 3 {
		t.Fatalf("offset = %d; курсор должен сдвинуться на 3 прочитанные строки, а не на 2 показанные", offset)
	}
}

func TestNullableStringKeepsEmptyDistinctFromValue(t *testing.T) {
	// Пустая строка и «замера нет» — разные вещи, и стирать разницу нельзя.
	if nullableString("") != nil {
		t.Fatal("пустая строка должна уезжать как null")
	}
	if nullableString("5 из 12") != "5 из 12" {
		t.Fatal("непустое значение должно уезжать как есть")
	}
}
