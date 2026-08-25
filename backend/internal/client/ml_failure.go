// ml_failure.go — отказ ML-сервиса со всем, что о нём известно на месте вызова.
//
// Раньше отказ схлопывался в один из четырёх сентинелов: 5xx превращался в
// голый ErrServer, 4xx — в строку «status 404», сетевой отказ — в текст
// net-ошибки. Тело неуспешного ответа при этом закрывалось непрочитанным,
// хотя именно в нём ML пишет диагноз (habitus/online/errors.py): какая стадия
// упала, какое исключение, что чинить. Причина терялась ровно на границе двух
// сервисов, и «поиск не работает» одинаково звучало для непонятой базы, для
// кончившейся квоты LLM и для ML_SERVICE_URL, смотрящего не туда.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxErrorBodyBytes — сколько байт тела неуспешного ответа читаем. Диагноз ML
// укладывается в пару сотен байт; ограничение защищает от чужого процесса,
// который на 404 отдаёт мегабайт HTML.
const maxErrorBodyBytes = 8 << 10

// MLFailure несёт причину отказа целиком. Sentinel-ы (ErrTimeout и прочие)
// остались кодом класса отказа — Unwrap отдаёт их наружу, поэтому весь
// существующий errors.Is продолжает работать без правок.
type MLFailure struct {
	Kind     error         // ErrTimeout / ErrUnavailable / ErrServer / ErrBadResponse
	Endpoint string        // "/search" — куда именно ходили
	Status   int           // HTTP-статус; 0, если ответа не было вовсе
	Elapsed  time.Duration // сколько реально прождали до отказа
	// Code/Stage/Hint/Timings приходят из структурного detail ML. Пустые, если
	// ответила не наша ручка (чужой процесс, прокси) — выдумывать их нельзя.
	Code    string
	Stage   string
	Hint    string
	Timings map[string]float64
	// Detail — человеческая причина: message из detail, строковый detail или
	// сырое тело ответа. Для сетевого отказа — текст ошибки транспорта.
	Detail string

	transport error // низкоуровневая ошибка, если отказ случился до ответа
}

func (f *MLFailure) Error() string {
	var b strings.Builder
	b.WriteString("ml ")
	b.WriteString(f.Endpoint)
	if f.Status != 0 {
		fmt.Fprintf(&b, " status %d", f.Status)
	}
	if f.Code != "" {
		b.WriteString(" code=" + f.Code)
	}
	if f.Stage != "" {
		b.WriteString(" stage=" + f.Stage)
	}
	if f.Kind != nil {
		b.WriteString(": " + f.Kind.Error())
	}
	if f.Detail != "" {
		b.WriteString(": " + f.Detail)
	}
	return b.String()
}

// Unwrap отдаёт и класс отказа, и исходную ошибку транспорта: errors.Is(err,
// ErrUnavailable) и errors.Is(err, context.DeadlineExceeded) работают оба.
func (f *MLFailure) Unwrap() []error {
	errs := make([]error, 0, 2)
	if f.Kind != nil {
		errs = append(errs, f.Kind)
	}
	if f.transport != nil {
		errs = append(errs, f.transport)
	}
	return errs
}

// mlDetail — разобранное тело неуспешного ответа. FastAPI кладёт диагноз в
// detail: объектом (наш habitus/online/errors.py) или строкой (HTTPException
// с текстом). Всё остальное — не наш сервис, и тело идёт в Detail как есть.
type mlDetail struct {
	Code    string             `json:"code"`
	Stage   string             `json:"stage"`
	Message string             `json:"message"`
	Hint    string             `json:"hint"`
	Timings map[string]float64 `json:"timings"`
}

// readMLDetail читает тело неуспешного ответа, не роняя вызов на нечитаемом
// или не-JSON теле: отсутствие диагноза — не повод потерять сам отказ.
func readMLDetail(body io.Reader) mlDetail {
	raw, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil || len(raw) == 0 {
		return mlDetail{}
	}

	var envelope struct {
		Detail json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || len(envelope.Detail) == 0 {
		return mlDetail{Message: compactBody(raw)}
	}

	var structured mlDetail
	if err := json.Unmarshal(envelope.Detail, &structured); err == nil && structured.Message != "" {
		return structured
	}
	var text string
	if err := json.Unmarshal(envelope.Detail, &text); err == nil {
		return mlDetail{Message: text}
	}
	// detail есть, но формы, которой мы не знаем (например, список ошибок
	// валидации pydantic) — отдаём как текст, а не молча теряем.
	return mlDetail{Message: compactBody(envelope.Detail)}
}

// compactBody схлопывает тело в одну строку: причина едет в лог и в SSE-событие,
// где многострочный HTML или трассировка только мешают.
func compactBody(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > 500 {
		text = text[:500] + "…"
	}
	return text
}

// kindOfStatus — класс отказа по HTTP-статусу; nil означает «ответ успешный».
// Одно место вместо четырёх копий `>= 500 / >= 400` по клиенту: пороги обязаны
// совпадать во всех ручках, иначе один и тот же 404 в разных вызовах поедет
// пользователю с разными кодами.
func kindOfStatus(status int) error {
	switch {
	case status >= 500:
		return ErrServer
	case status >= 400:
		return ErrBadResponse
	default:
		return nil
	}
}

// kindOfTransportError отличает исчерпанный бюджет от недоступного сервиса.
// Проверяется контекст вызова, а не текст ошибки: http.Client возвращает на
// дедлайне обычную *url.Error, и по ней «не успели» неотличимо от «некуда
// стучаться» — а чинятся эти два случая по-разному.
func kindOfTransportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ErrTimeout
	}
	return ErrUnavailable
}

// httpFailure — отказ, у которого ответ есть: читаем из него диагноз.
func httpFailure(kind error, endpoint string, resp *http.Response, started time.Time) *MLFailure {
	detail := readMLDetail(resp.Body)
	return &MLFailure{
		Kind: kind, Endpoint: endpoint, Status: resp.StatusCode,
		Elapsed: time.Since(started), Code: detail.Code, Stage: detail.Stage,
		Hint: detail.Hint, Timings: detail.Timings, Detail: detail.Message,
	}
}

// transportFailure — отказ до ответа: соединение, дедлайн, разрыв потока.
func transportFailure(kind error, endpoint string, err error, started time.Time) *MLFailure {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	return &MLFailure{
		Kind: kind, Endpoint: endpoint, Elapsed: time.Since(started),
		Detail: detail, transport: err,
	}
}
