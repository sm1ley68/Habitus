package middleware

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/observability"
)

// RateLimiter — счётчик LLM-запросов по user_id со скользящим окном в час.
// In-memory map — как и in-memory лок стрима в
// service/search_stream_service.go (TryLock/Unlock): корректно для ОДНОЙ
// реплики backend'а, не для горизонтального масштабирования. При нескольких
// репликах каждая будет считать свою половину трафика пользователя — это
// сознательный компромисс этого прохода, тот же, что уже принят для лока
// стрима.
type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	now      func() time.Time // подменяемый источник времени — тесты двигают окно без time.Sleep
	requests map[uuid.UUID][]time.Time
}

// NewRateLimiter — limit <= 0 трактуется как «ничего не пропускать»: сам по
// себе лимитер не должен молча превращаться в заглушку. Проводка в app.go при
// этом подставляет дефолт вместо неположительного значения из конфига, так что
// из HTTP эта ветка недостижима — она страхует прямое конструирование.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit: limit, window: window, now: time.Now,
		requests: make(map[uuid.UUID][]time.Time),
	}
}

// Allow регистрирует попытку пользователя userID и возвращает, разрешена ли
// она, и — если нет — через сколько восстановится хотя бы одно место в окне.
// Скользящее окно: считаем запросы моложе `window` относительно ТЕКУЩЕГО
// момента (а не выравниваем на границы часа), иначе пользователь получал бы
// двойной лимит на стыке часов.
func (r *RateLimiter) Allow(userID uuid.UUID) (allowed bool, retryAfter time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	cutoff := now.Add(-r.window)

	kept := r.requests[userID][:0]
	for _, t := range r.requests[userID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}

	if r.limit <= 0 || len(kept) >= r.limit {
		r.setWindow(userID, kept)
		if len(kept) == 0 {
			return false, r.window
		}
		// самый старый запрос в окне выйдет за его границу первым — тогда и
		// освободится место.
		return false, kept[0].Add(r.window).Sub(now)
	}

	kept = append(kept, now)
	r.setWindow(userID, kept)
	return true, 0
}

// setWindow сохраняет окно пользователя, удаляя запись целиком, когда окно
// опустело: иначе карта монотонно растёт по числу когда-либо заходивших
// пользователей и за месяцы аптайма превращается в медленную течь памяти.
// Вызывается под уже взятым r.mu.
func (r *RateLimiter) setWindow(userID uuid.UUID, window []time.Time) {
	if len(window) == 0 {
		delete(r.requests, userID)
		return
	}
	r.requests[userID] = window
}

// Track — сколько пользователей сейчас в карте. Нужен тестам, чтобы проверить,
// что окна вычищаются, а не копятся.
func (r *RateLimiter) Track() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// RateLimitLLM — middleware для LLM-ручек (POST .../messages/stream и
// POST .../ask/stream). Ставится ПОСЛЕ Auth: читает user_id из
// fiber.Locals, который заполняет Auth. При превышении — 429 и честное
// сообщение по-русски о том, через сколько минут лимит восстановится, плюс
// стандартный заголовок Retry-After в секундах.
func RateLimitLLM(limiter *RateLimiter) fiber.Handler {
	return func(c *fiber.Ctx) error {
		userID := UserID(c)
		allowed, retryAfter := limiter.Allow(userID)
		if !allowed {
			observability.Default.IncRateLimited()
			c.Set(fiber.HeaderRetryAfter, strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
			minutes := int(math.Ceil(retryAfter.Minutes()))
			if minutes < 1 {
				minutes = 1
			}
			return apperr.RateLimited(fmt.Sprintf(
				"Превышен лимит запросов к ИИ (%d в час). Попробуйте снова через %d мин.",
				limiter.limit, minutes,
			))
		}
		return c.Next()
	}
}
