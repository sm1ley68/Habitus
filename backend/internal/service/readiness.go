// readiness.go — проба готовности шлюза принимать трафик. Отдельно от
// liveness: процесс может быть жив и при этом бесполезен, если под ним нет
// Postgres или ML-сервиса. Оркестратор должен различать эти два состояния —
// liveness перезапускает контейнер, readiness лишь снимает его с балансировки.
package service

import (
	"context"
	"sync"
	"time"
)

// Probe — одна проверка зависимости. nil — зависимость отвечает.
type Probe func(context.Context) error

type ReadinessService struct {
	probes  map[string]Probe
	timeout time.Duration
}

// NewReadinessService. Неположительный таймаут означает «без собственного
// предела» — тогда границу задаёт только контекст запроса.
func NewReadinessService(timeout time.Duration, probes map[string]Probe) *ReadinessService {
	return &ReadinessService{probes: probes, timeout: timeout}
}

// Check прогоняет пробы ПАРАЛЛЕЛЬНО: последовательный прогон складывал бы
// таймауты, и при двух мёртвых зависимостях readiness сам отвечал бы дольше,
// чем его готов ждать оркестратор. Возвращает ok и карту «имя → ok либо текст
// причины»: 503 без причины — та же заглушка, только с другим кодом.
func (s *ReadinessService) Check(ctx context.Context) (bool, map[string]string) {
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	checks := make(map[string]string, len(s.probes))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, probe := range s.probes {
		wg.Add(1)
		go func(name string, probe Probe) {
			defer wg.Done()
			result := "ok"
			if err := probe(ctx); err != nil {
				result = err.Error()
			}
			mu.Lock()
			checks[name] = result
			mu.Unlock()
		}(name, probe)
	}
	wg.Wait()

	ok := true
	for _, result := range checks {
		if result != "ok" {
			ok = false
		}
	}
	return ok, checks
}
