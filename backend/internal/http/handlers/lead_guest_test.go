package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// fakeLeadSender — заглушка leadSender. Send и ListForSeller паникуют:
// в тестах этого файла цель либо не проходит проверку (дальше хода нет), либо
// заявка не должна дойти до сервиса до апгрейда гостя — если дошла, это и есть
// найденный порядок-баг.
type fakeLeadSender struct {
	listing domain.OwnerListing
	err     error
}

func (f fakeLeadSender) ResolveTarget(context.Context, uuid.UUID, string) (domain.OwnerListing, error) {
	return f.listing, f.err
}

func (f fakeLeadSender) Send(context.Context, uuid.UUID, string, service.LeadInput) (domain.Lead, error) {
	panic("Send не должен вызываться в этих тестах")
}

func (f fakeLeadSender) ListForSeller(context.Context, uuid.UUID, int, int) ([]domain.Lead, int, error) {
	panic("ListForSeller не используется в этих тестах")
}

// newGuestLeadApp собирает хендлер с гостем в Locals. auth — НУЛЕВОЙ намеренно:
// обе проверяемые в этом файле ветки обязаны отработать до похода в
// UpgradeGuest. Если ветка «протечёт» дальше, тест упадёт паникой на nil —
// это и есть проверка порядка.
func newGuestLeadApp(leads leadSender) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: middleware.ErrorHandler})
	h := NewLeadHandler(leads, nil, false, nil)
	app.Post("/objects/:object_id/lead", func(c *fiber.Ctx) error {
		c.Locals(middleware.UserIDLocalsKey, uuid.New())
		c.Locals(middleware.IsGuestLocalsKey, true)
		return c.Next()
	}, h.Send)
	return app
}

// publishedTarget — цель, которую ResolveTarget пропускает: объявление
// опубликовано и принадлежит не тому, кто отправляет.
func publishedTarget() fakeLeadSender {
	return fakeLeadSender{listing: domain.OwnerListing{
		ID: uuid.New(), UserID: uuid.New(), Status: "published", ExternalID: "cian_1",
	}}
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
	status, got := postLead(t, newGuestLeadApp(publishedTarget()),
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
	// leads тут тоже нулевой: до ResolveTarget форма не должна дойти вовсе.
	status, got := postLead(t, newGuestLeadApp(nil),
		`{"name":"Иван","contact":"   ","register":{"email":"a@example.test","password":"password1"}}`)

	if status != fiber.StatusBadRequest {
		t.Fatalf("статус = %d, ожидался 400", status)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil || envelope["code"] != "validation_error" {
		t.Fatalf("ожидался validation_error, получено %v", got)
	}
}

// Объект сняли с публикации между открытием паспорта и отправкой формы.
// Гость с корректной формой и блоком register не должен получить аккаунт под
// заявку, которая тут же откажет: цель проверяется ДО UpgradeGuest, поэтому
// ни Set-Cookie, ни смены сессии тут быть не должно.
func TestLeadSendRejectsWithdrawnTargetBeforeUpgradingGuest(t *testing.T) {
	app := newGuestLeadApp(fakeLeadSender{err: apperr.LeadTargetNotFound()})

	req := httptest.NewRequest("POST", "/objects/cian_1/lead", strings.NewReader(
		`{"name":"Иван","contact":"+7 999 000-00-00","register":{"email":"a@example.test","password":"password1"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("запрос: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != fiber.StatusNotFound {
		t.Fatalf("статус = %d, ожидался 404 (%s)", resp.StatusCode, raw)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("разбор тела: %v (%s)", err, raw)
	}
	envelope, _ := got["error"].(map[string]any)
	if envelope == nil || envelope["code"] != "lead_target_not_found" {
		t.Fatalf("ожидался lead_target_not_found, получено %v", got)
	}
	if resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("Set-Cookie выставлен на отказе: %q — гость не должен стать аккаунтом", resp.Header.Get("Set-Cookie"))
	}
}
