// Package apperr defines the unified error envelope used across all REST handlers,
// matching frontend/Пайплайн фронт.md §1 and §6.
package apperr

import "net/http"

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
