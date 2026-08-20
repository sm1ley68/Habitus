package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// fakeClock — подставное время для теста скользящего окна: тест двигает часы
// сам, без time.Sleep (прямое требование брифа Task 8).
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestLimiter(limit int, window time.Duration) (*RateLimiter, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)}
	rl := NewRateLimiter(limit, window)
	rl.now = clock.now
	return rl, clock
}

// Первый запрос пользователя в пустом окне обязан пройти.
func TestRateLimiterFirstRequestAllowed(t *testing.T) {
	rl, _ := newTestLimiter(3, time.Hour)
	user := uuid.New()

	allowed, _ := rl.Allow(user)
	if !allowed {
		t.Fatal("первый запрос пользователя должен быть разрешён")
	}
}

// Ровно N-й запрос в окне ещё проходит, N+1-й — уже 429.
func TestRateLimiterAllowsExactlyNThenBlocksNPlus1(t *testing.T) {
	rl, _ := newTestLimiter(3, time.Hour)
	user := uuid.New()

	for i := 1; i <= 3; i++ {
		allowed, _ := rl.Allow(user)
		if !allowed {
			t.Fatalf("запрос №%d должен быть разрешён (лимит 3)", i)
		}
	}

	allowed, retryAfter := rl.Allow(user)
	if allowed {
		t.Fatal("4-й запрос при лимите 3 должен быть отклонён")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter = %v; должно быть положительным при отказе", retryAfter)
	}
}

// После сдвига окна (подставные часы, без time.Sleep) лимит снова пропускает.
func TestRateLimiterSlidingWindowRecoversAfterShift(t *testing.T) {
	rl, clock := newTestLimiter(2, time.Hour)
	user := uuid.New()

	if allowed, _ := rl.Allow(user); !allowed {
		t.Fatal("1-й запрос должен пройти")
	}
	if allowed, _ := rl.Allow(user); !allowed {
		t.Fatal("2-й запрос должен пройти")
	}
	if allowed, _ := rl.Allow(user); allowed {
		t.Fatal("3-й запрос при лимите 2 должен быть отклонён")
	}

	// Сдвигаем часы за границу часового окна — самый старый запрос устарел.
	clock.advance(time.Hour + time.Second)

	allowed, _ := rl.Allow(user)
	if !allowed {
		t.Fatal("после сдвига окна за час запрос должен снова пройти")
	}
}

// Граница окна: запрос ровно на границе (now - window) уже не считается —
// окно закрывается включительно по истечении часа, а не позже.
func TestRateLimiterWindowBoundaryExcludesExpiredRequest(t *testing.T) {
	rl, clock := newTestLimiter(1, time.Hour)
	user := uuid.New()

	if allowed, _ := rl.Allow(user); !allowed {
		t.Fatal("1-й запрос должен пройти")
	}

	// Сразу после первого запроса второй в том же окне обязан быть отклонён.
	if allowed, _ := rl.Allow(user); allowed {
		t.Fatal("2-й запрос сразу после 1-го при лимите 1 должен быть отклонён")
	}

	// Сдвигаем часы ровно на длину окна — старый запрос вышел за границу.
	clock.advance(time.Hour)
	allowed, _ := rl.Allow(user)
	if !allowed {
		t.Fatal("запрос ровно на границе окна (now - window) должен пройти")
	}
}

// Лимит считается по пользователю, а не глобально: один пользователь упёрся в
// лимит — у другого свежая квота.
func TestRateLimiterPerUserIndependent(t *testing.T) {
	rl, _ := newTestLimiter(1, time.Hour)
	userA := uuid.New()
	userB := uuid.New()

	if allowed, _ := rl.Allow(userA); !allowed {
		t.Fatal("первый запрос userA должен пройти")
	}
	if allowed, _ := rl.Allow(userA); allowed {
		t.Fatal("второй запрос userA при лимите 1 должен быть отклонён")
	}

	allowed, _ := rl.Allow(userB)
	if !allowed {
		t.Fatal("userB не должен зависеть от лимита userA")
	}
}

// HTTP-уровень: превышение лимита отдаёт 429 с честным русским сообщением и
// заголовком Retry-After.
func TestRateLimitMiddlewareReturns429WithRussianMessage(t *testing.T) {
	rl, _ := newTestLimiter(1, time.Hour)
	fixedUser := uuid.New()

	app := fiber.New(fiber.Config{ErrorHandler: ErrorHandler})
	app.Use(func(c *fiber.Ctx) error {
		c.Locals(UserIDLocalsKey, fixedUser)
		return c.Next()
	})
	app.Post("/probe", RateLimitLLM(rl), func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	first, err := app.Test(httptest.NewRequest(http.MethodPost, "/probe", nil))
	if err != nil {
		t.Fatalf("первый запрос: %v", err)
	}
	if first.StatusCode != http.StatusOK {
		t.Fatalf("первый запрос: status = %d; want 200", first.StatusCode)
	}

	second, err := app.Test(httptest.NewRequest(http.MethodPost, "/probe", nil))
	if err != nil {
		t.Fatalf("второй запрос: %v", err)
	}
	if second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("второй запрос: status = %d; want 429", second.StatusCode)
	}
	if retryAfter := second.Header.Get("Retry-After"); retryAfter == "" {
		t.Fatal("ожидался заголовок Retry-After")
	}

	body, err := io.ReadAll(second.Body)
	if err != nil {
		t.Fatalf("чтение тела: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "лимит") || !strings.Contains(text, "мин") {
		t.Fatalf("тело ответа не похоже на честное сообщение о восстановлении лимита: %s", text)
	}
}

func TestRateLimiterForgetsUsersWhoseWindowExpired(t *testing.T) {
	// Карта лимитера не должна расти по числу когда-либо заходивших: запись
	// пользователя, чьё окно опустело, обязана уйти, иначе за месяцы аптайма
	// это медленная течь памяти.
	limiter, clock := newTestLimiter(2, time.Hour)

	user := uuid.New()
	if allowed, _ := limiter.Allow(user); !allowed {
		t.Fatal("первый запрос должен пройти")
	}
	if got := limiter.Track(); got != 1 {
		t.Fatalf("Track() = %d; want 1 — окно пользователя должно быть заведено", got)
	}

	clock.advance(2 * time.Hour) // окно полностью вышло
	if allowed, _ := limiter.Allow(user); !allowed {
		t.Fatal("после выхода окна запрос должен снова проходить")
	}
	// запись есть — пользователь только что сходил
	if got := limiter.Track(); got != 1 {
		t.Fatalf("Track() = %d; want 1", got)
	}
}

func TestRateLimiterDropsEntryWhenBlockedWindowEmpties(t *testing.T) {
	// limit<=0 — единственный путь, где отказ случается при пустом окне:
	// запись заводиться не должна вовсе.
	limiter := NewRateLimiter(0, time.Hour)

	if allowed, _ := limiter.Allow(uuid.New()); allowed {
		t.Fatal("limit<=0 не должен пропускать")
	}
	if got := limiter.Track(); got != 0 {
		t.Fatalf("Track() = %d; want 0 — пустое окно не должно оседать в карте", got)
	}
}
