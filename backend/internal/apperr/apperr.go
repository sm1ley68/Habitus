// Package apperr defines the unified error envelope used across all REST handlers,
// matching frontend/Пайплайн фронт.md §1 and §6.
package apperr

import (
	"fmt"
	"net/http"
)

type Error struct {
	Status  int
	Code    string
	Message string
	// Cause/Hint — техническая улика и что с ней делать. Отдельно от Message,
	// потому что читателей два: пользователю хватает Message, а разработчику
	// без причины приходится восстанавливать её по логам двух контейнеров.
	// Пустые поля означают «улики нет» и в конверт не попадают вовсе.
	Cause string
	Hint  string
	// Param — поле запроса, из-за которого отказ. Нужен Partner API: конверт
	// ошибки без имени поля заставляет разработчика гадать, что именно в его
	// теле не так.
	Param string
}

func (e *Error) Error() string { return e.Message }

// WithCause возвращает КОПИЮ с добавленной причиной: обогащение одного отказа
// не должно протекать в соседний запрос через общее значение.
func (e *Error) WithCause(cause string) *Error {
	copied := *e
	copied.Cause = cause
	return &copied
}

// WithHint возвращает копию с подсказкой «что чинить».
func (e *Error) WithHint(hint string) *Error {
	copied := *e
	copied.Hint = hint
	return &copied
}

// WithParam возвращает копию с именем поля, вызвавшего отказ.
func (e *Error) WithParam(param string) *Error {
	copied := *e
	copied.Param = param
	return &copied
}

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func Validation(message string) *Error {
	return New(http.StatusBadRequest, "validation_error", message)
}

func Unauthorized() *Error {
	return New(http.StatusUnauthorized, "unauthorized", "Нет / истёк токен сессии")
}

func ChatNotFound() *Error {
	return New(http.StatusNotFound, "chat_not_found", "Чат с указанным ID не найден")
}

func ObjectNotFound() *Error {
	return New(http.StatusNotFound, "object_not_found", "Объект недвижимости не найден")
}

func StreamInProgress() *Error {
	return New(http.StatusConflict, "stream_in_progress", "Стрим для этого чата уже выполняется")
}

func ObjectStreamInProgress() *Error {
	return New(http.StatusConflict, "stream_in_progress", "Стрим для этого объекта и чата уже выполняется")
}

func Internal(message string) *Error {
	return New(http.StatusInternalServerError, "internal_error", message)
}

func RateLimited(message string) *Error {
	return New(http.StatusTooManyRequests, "rate_limited", message)
}

// GuestForbidden — гостю сюда нельзя. Не 401: сессия у него настоящая, дело
// в том, что действие требует аккаунта, на который можно ответить.
func GuestForbidden(message string) *Error {
	return New(http.StatusForbidden, "guest_forbidden", message)
}

func CianURLInvalid() *Error {
	return New(http.StatusBadRequest, "cian_url_invalid",
		"Это не похоже на ссылку на объявление Циана. Скопируйте адрес страницы объявления целиком")
}

func CianOfferNotFound() *Error {
	return New(http.StatusNotFound, "cian_offer_not_found",
		"Циан не отдал такое объявление — возможно, оно снято с публикации")
}

func CianUnavailable() *Error {
	return New(http.StatusServiceUnavailable, "cian_unavailable",
		"Циан сейчас не отдаёт данные. Попробуйте позже или заполните карточку вручную")
}

func ListingClaimedByOther() *Error {
	return New(http.StatusConflict, "listing_claimed_by_other",
		"Это объявление уже привязано к другому аккаунту")
}

func OwnerListingNotFound() *Error {
	return New(http.StatusNotFound, "owner_listing_not_found", "Объявление не найдено")
}

func OwnerListingInvalid(field, message string) *Error {
	return New(http.StatusBadRequest, "owner_listing_invalid", message+" (поле: "+field+")")
}

func PhotoTooLarge(maxMB int) *Error {
	return New(http.StatusBadRequest, "photo_too_large",
		fmt.Sprintf("Фотография больше %d МБ", maxMB))
}

func PhotoUnsupportedFormat() *Error {
	return New(http.StatusBadRequest, "photo_unsupported_format",
		"Поддерживаются только JPEG, PNG и WebP")
}

func PhotoLimitExceeded(max int) *Error {
	return New(http.StatusBadRequest, "photo_limit_exceeded",
		fmt.Sprintf("К объявлению можно приложить не больше %d фотографий", max))
}

func LeadTargetNotFound() *Error {
	return New(http.StatusNotFound, "lead_target_not_found",
		"По этому объекту заявку оставить нельзя — свяжитесь с продавцом у источника")
}

func LeadAlreadySent() *Error {
	return New(http.StatusConflict, "lead_already_sent",
		"Вы уже отправляли заявку по этому объявлению — продавец её видит")
}

func LeadToSelf() *Error {
	return New(http.StatusBadRequest, "lead_to_self",
		"Это ваше собственное объявление")
}

// RegistrationRequired — не отказ, а приглашение. Гость дошёл до заявки, и
// именно здесь аккаунт впервые нужен по делу: продавцу нужно, кому ответить.
// По этому коду фронт открывает регистрацию ПРЯМО В ФОРМЕ заявки, не теряя
// заполненного, и повторяет запрос с блоком register.
func RegistrationRequired() *Error {
	return New(http.StatusForbidden, "registration_required",
		"Заведите аккаунт, чтобы отправить заявку — продавцу нужно, кому ответить. "+
			"Всё, что вы уже нашли и сохранили, останется при вас")
}

// --- Partner API (B2B) ---
//
// Отдельные коды, а не переиспользование B2C: у интеграции другой читатель.
// Человеку сообщение показывают в интерфейсе, а разработчику партнёра оно
// попадает в лог, и «Нет / истёк токен сессии» там не значит ничего.

// PartnerKeyInvalid — общий ответ на любой негодный ключ: не тот формат, не
// найден, не сошёлся секрет. Разные сообщения на эти случаи позволяли бы
// перебором выяснять, какие префиксы существуют.
func PartnerKeyInvalid() *Error {
	return New(http.StatusUnauthorized, "invalid_api_key",
		"Ключ API не распознан. Проверьте заголовок Authorization: Bearer hab_live_…")
}

func PartnerKeyMissing() *Error {
	return New(http.StatusUnauthorized, "api_key_missing",
		"Нужен ключ API: заголовок Authorization: Bearer hab_live_…")
}

func PartnerKeyRevoked() *Error {
	return New(http.StatusUnauthorized, "api_key_revoked",
		"Этот ключ отозван. Выпустите новый в кабинете партнёра")
}

func PartnerKeyExpired() *Error {
	return New(http.StatusUnauthorized, "api_key_expired",
		"Срок действия ключа истёк. Выпустите новый в кабинете партнёра")
}

// PartnerIPNotAllowed — ключ рабочий, но пришёл не с того адреса. Отдельный
// код, а не общий 403: партнёру нужно понять, что чинить — права или сеть.
func PartnerIPNotAllowed() *Error {
	return New(http.StatusForbidden, "ip_not_allowed",
		"Этот ключ работает только с согласованных адресов").
		WithHint("Проверьте, с какого адреса уходит запрос, и пришлите его нам, " +
			"если он изменился")
}

func PartnerSuspended() *Error {
	return New(http.StatusForbidden, "partner_suspended",
		"Доступ партнёра приостановлен. Напишите нам, чтобы восстановить его")
}

// PartnerScopeRequired называет недостающий scope прямо в сообщении: без
// имени права разработчику остаётся угадывать, чего не хватило.
func PartnerScopeRequired(scope string) *Error {
	return New(http.StatusForbidden, "insufficient_scope",
		"Ключу не хватает права "+scope).WithParam("scope")
}

func PartnerQuotaExceeded(message string) *Error {
	return New(http.StatusTooManyRequests, "rate_limit_exceeded", message)
}

// IdempotencyKeyReuse — тот же ключ идемпотентности с другим телом. Это
// ошибка клиента: отдать ему чужой сохранённый ответ было бы хуже отказа.
func IdempotencyKeyReuse() *Error {
	return New(http.StatusConflict, "idempotency_key_reuse",
		"Этот Idempotency-Key уже использован с другим телом запроса").
		WithParam("Idempotency-Key")
}

func SearchNotFound() *Error {
	return New(http.StatusNotFound, "search_not_found",
		"Поиск не найден или уже удалён по сроку хранения")
}

func WebhookNotFound() *Error {
	return New(http.StatusNotFound, "webhook_not_found", "Вебхук не найден")
}
