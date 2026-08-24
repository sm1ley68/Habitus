package cian

import (
	"sync"
	"testing"
	"time"
)

func TestRateLimiterCapsBurst(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(3, func() time.Time { return now })

	for i := 0; i < 3; i++ {
		if !limiter.Allow() {
			t.Fatalf("запрос %d должен был пройти", i+1)
		}
	}
	if limiter.Allow() {
		t.Fatal("четвёртый запрос за ту же минуту должен быть отклонён")
	}
}

func TestRateLimiterRecoversAfterWindow(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(1, func() time.Time { return now })

	if !limiter.Allow() {
		t.Fatal("первый запрос должен пройти")
	}
	if limiter.Allow() {
		t.Fatal("второй запрос в том же окне должен быть отклонён")
	}
	now = now.Add(61 * time.Second)
	if !limiter.Allow() {
		t.Fatal("после окна лимит должен восстановиться")
	}
}

func TestRateLimiterIsConcurrencySafe(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	limiter := NewRateLimiter(50, func() time.Time { return now })

	var mu sync.Mutex
	allowed := 0
	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if limiter.Allow() {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("пропущено %d запросов вместо 50", allowed)
	}
}

func TestUserQuotaIsPerUser(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	quota := NewUserQuota(2, func() time.Time { return now })

	if !quota.Allow("alice") || !quota.Allow("alice") {
		t.Fatal("две попытки alice должны пройти")
	}
	if quota.Allow("alice") {
		t.Fatal("третья попытка alice должна быть отклонена")
	}
	if !quota.Allow("bob") {
		t.Fatal("квота bob не должна зависеть от alice")
	}
}

func TestUserQuotaWindowSlides(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	quota := NewUserQuota(1, func() time.Time { return now })

	if !quota.Allow("alice") {
		t.Fatal("первая попытка должна пройти")
	}
	now = now.Add(59 * time.Minute)
	if quota.Allow("alice") {
		t.Fatal("внутри часа вторая попытка должна быть отклонена")
	}
	now = now.Add(2 * time.Minute)
	if !quota.Allow("alice") {
		t.Fatal("за пределами часа лимит должен восстановиться")
	}
}
