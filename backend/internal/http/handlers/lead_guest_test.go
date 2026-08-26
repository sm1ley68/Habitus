package handlers

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/http/middleware"
)

// newGuestLeadApp собирает хендлер с НУЛЕВЫМИ зависимостями намеренно: обе
// проверяемые ветки обязаны отработать до похода в сервис заявок и в auth.
// Если ветка «протечёт» дальше, тест упадёт паникой на nil — это и есть
// проверка порядка.
func newGuestLeadApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: middleware.ErrorHandler})
	h := NewLeadHandler(nil, nil, false, nil)
	app.Post("/objects/:object_id/lead", func(c *fiber.Ctx) error {
		c.Locals(middleware.UserIDLocalsKey, uuid.New())
		c.Locals(middleware.IsGuestLocalsKey, true)
		return c.Next()
	}, h.Send)
	return app
}

func postLead(t *testing.T, app *fiber.App, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("POST", "/objects/cian_1/lead", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("разбор тела: %v (%s)", err, raw)
	}
	return resp.StatusCode, got
}

// Гость без блока register получает ПРИГЛАШЕНИЕ зарегистрироваться, а не
// глухой отказ: по этому коду фронт раскрывает поля email/пароля в той же
// форме и повторяет запрос.
func TestLeadSendInvitesGuestToRegister(t *testing.T) {
	status, got := postLead(t, newGuestLeadApp(),
		`{"name":"Иван","contact":"+7 999 000-00-00"}`)

	if status != fiber.StatusForbidden {
		t.Fatalf("статус = %d, ожидался 403", status)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil {
		t.Fatalf("ответ вне конверта ошибки: %v", got)
	}
	if envelope["code"] != "registration_required" {
		t.Fatalf("code = %v, ожидался registration_required", envelope["code"])
	}
	message, _ := envelope["message"].(string)
	if message == "" {
		t.Fatal("пустое сообщение: гость не поймёт, что ему предлагают")
	}
}

// Форма проверяется ДО регистрации: иначе человек с пустым телефоном сначала
// получил бы аккаунт и только потом — ошибку поля. Нулевой auth в хендлере
// это и доказывает: дойди сюда регистрация, тест упал бы паникой.
func TestLeadSendValidatesFormBeforeRegistering(t *testing.T) {
	status, got := postLead(t, newGuestLeadApp(),
		`{"name":"Иван","contact":"   ","register":{"email":"a@example.test","password":"password1"}}`)

	if status != fiber.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", status)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil || envelope["code"] != "validation_error" {
		t.Fatalf("ожидался validation_error, получено %v", got)
	}
}
