package handlers

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/domain"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

// guestUpgrader — часть AuthService, нужная заявке. Обособленный интерфейс:
// AuthService держит конкретные репозитории, и без него хендлер нельзя было бы
// проверить без поднятой БД.
type guestUpgrader interface {
	UpgradeGuest(ctx context.Context, guestID uuid.UUID, email, password, name string) (domain.User, string, time.Time, error)
}

// leadSender — часть LeadService, нужная хендлеру. Обособленный интерфейс по
// той же причине, что и guestUpgrader: без него порядок «сначала цель, потом
// апгрейд гостя» нельзя было бы проверить без поднятой БД.
type leadSender interface {
	ResolveTarget(ctx context.Context, buyerID uuid.UUID, externalID string) (domain.OwnerListing, error)
	Send(ctx context.Context, buyerID uuid.UUID, externalID string, in service.LeadInput) (domain.Lead, error)
	ListForSeller(ctx context.Context, sellerID uuid.UUID, limit, offset int) ([]domain.Lead, int, error)
}

type LeadHandler struct {
	leads        leadSender
	auth         guestUpgrader
	cookieSecure bool
	events       *service.EventRecorder
}

func NewLeadHandler(leads leadSender, auth guestUpgrader, cookieSecure bool, events *service.EventRecorder) *LeadHandler {
	return &LeadHandler{leads: leads, auth: auth, cookieSecure: cookieSecure, events: events}
}

// leadRegisterRequest — регистрация прямо в форме заявки. Пароль здесь тот же,
// что и в /auth/register, и проверяется тем же UpgradeGuest.
type leadRegisterRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type leadRequest struct {
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Message string `json:"message"`
	// Register присылает гость. Отсутствует — гость получает приглашение
	// зарегистрироваться (403 registration_required), а не глухой отказ.
	Register *leadRegisterRequest `json:"register"`
}

// LeadDTO — форма заявки в ответах. Контакт покупателя тут есть (продавец за
// ним и пришёл), контакта продавца нет нигде.
func LeadDTO(l domain.Lead) fiber.Map {
	return fiber.Map{
		"id":          l.ID,
		"listing_id":  l.ListingID,
		"external_id": l.ExternalID,
		"address":     l.Address,
		"name":        l.Name,
		"contact":     l.Contact,
		"message":     l.Message,
		"created_at":  l.CreatedAt,
	}
}

const (
	leadsDefaultLimit = 20
	leadsMaxLimit     = 100
)

// List implements GET /api/v1/owner/leads?limit=&offset= — входящие заявки
// продавца, свежие сверху. Продавец берётся из сессии, а не из параметров.
func (h *LeadHandler) List(c *fiber.Ctx) error {
	limit, offset := parseLimitOffset(c, leadsDefaultLimit)
	if limit > leadsMaxLimit {
		limit = leadsMaxLimit
	}

	rows, total, err := h.leads.ListForSeller(c.Context(), middleware.UserID(c), limit, offset)
	if err != nil {
		return err
	}
	leads := make([]fiber.Map, 0, len(rows))
	for _, l := range rows {
		leads = append(leads, LeadDTO(l))
	}
	return c.JSON(fiber.Map{"leads": leads, "count": len(leads), "total": total})
}

// Send implements POST /objects/{object_id}/lead.
//
// Заявка от гостя, которого через месяц вычистит свипер, продавцу бесполезна —
// но отказывать здесь неправильно: это ровно та точка, где аккаунт впервые
// нужен по делу. Поэтому гостю не говорят «нельзя», а заводят аккаунт ТЕМ ЖЕ
// запросом: отдельный поход на регистрацию потерял бы заполненную форму, а
// вместе с ней и заявку.
func (h *LeadHandler) Send(c *fiber.Ctx) error {
	var req leadRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}

	// Поля заявки проверяются ДО регистрации: иначе человек с пустым телефоном
	// сначала получил бы аккаунт и только потом — ошибку формы.
	input, err := service.ValidateLeadInput(service.LeadInput{
		Name: req.Name, Contact: req.Contact, Message: req.Message,
	})
	if err != nil {
		return err
	}

	userID := middleware.UserID(c)

	// Цель проверяется ДО апгрейда гостя: объект могли снять с публикации
	// между открытием паспорта и отправкой формы, и заводить аккаунт ради
	// заявки, которая тут же откажет 404, не нужно. id покупателя апгрейд не
	// меняет, так что проверка тем же userID остаётся верной и после него.
	if _, err := h.leads.ResolveTarget(c.Context(), userID, objectID); err != nil {
		return err
	}

	registered := false
	if middleware.IsGuest(c) {
		if req.Register == nil || req.Register.Email == "" || req.Register.Password == "" {
			// Приглашение, а не отказ: фронт по этому коду раскрывает поля
			// email/пароля в той же форме и повторяет запрос.
			return apperr.RegistrationRequired()
		}
		// Имя из заявки становится именем аккаунта — отдельное поле спрашивать
		// незачем, человек его уже ввёл.
		u, token, expiresAt, err := h.auth.UpgradeGuest(c.Context(), userID,
			req.Register.Email, req.Register.Password, input.Name)
		if err != nil {
			return err
		}
		setSessionCookie(c, token, expiresAt, h.cookieSecure)
		userID = u.ID
		registered = true
		// Locals не обновляются после апгрейда в том же запросе: без этого
		// recordEvent ниже прочитал бы is_guest=true у уже зарегистрированного.
		c.Locals(middleware.IsGuestLocalsKey, false)
	}

	lead, err := h.leads.Send(c.Context(), userID, objectID, input)
	if err != nil {
		return err
	}

	// Регистрация из формы заявки — тот же шаг воронки, что и обычный апгрейд
	// гостя, и считаться должен вместе с ним, иначе конверсия гостей в аккаунты
	// окажется занижена ровно на самых ценных.
	if registered {
		h.events.Record(domain.ProductEvent{UserID: userID, Kind: service.EventGuestUpgraded,
			Props: map[string]any{"source": "lead_form"}})
	}
	recordEvent(c, h.events, service.EventLeadSent, nil, objectID,
		map[string]any{"has_message": req.Message != "", "registered_inline": registered})

	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"lead": LeadDTO(lead),
		// registered говорит фронту, что сессия сменилась и гость стал
		// аккаунтом: перечитывать /me ради одного флага незачем.
		"registered": registered,
	})
}
