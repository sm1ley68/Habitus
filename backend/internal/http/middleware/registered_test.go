package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func newRegisteredApp(isGuest bool) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	app.Get("/protected", func(c *fiber.Ctx) error {
		c.Locals(UserIDLocalsKey, uuid.New())
		c.Locals(IsGuestLocalsKey, isGuest)
		return c.Next()
	}, RequireRegistered(), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusNoContent)
	})
	return app
}

func TestRequireRegisteredLetsRegisteredThrough(t *testing.T) {
	resp, err := newRegisteredApp(false).Test(httptest.NewRequest("GET", "/protected", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("статус = %d, ожидался 204", resp.StatusCode)
	}
}

// Гостю тут нельзя, но отказ обязан объяснять, что делать: 403 с кодом
// guest_forbidden — это точка регистрации, а не поломка.
func TestRequireRegisteredBlocksGuestWithActionableCode(t *testing.T) {
	resp, err := newRegisteredApp(true).Test(httptest.NewRequest("GET", "/protected", nil))
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("статус = %d, ожидался 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var got struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("разбор конверта: %v (%s)", err, body)
	}
	if got.Error.Code != "guest_forbidden" {
		t.Fatalf("code = %q, ожидался guest_forbidden", got.Error.Code)
	}
	if got.Error.Message == "" {
		t.Fatal("пустое сообщение: гость не поймёт, что от него хотят")
	}
}
