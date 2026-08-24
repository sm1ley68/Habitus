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
}

func (e *Error) Error() string { return e.Message }

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
