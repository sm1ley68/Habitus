// session_sweeper.go — периодическая чистка протухших сессий. GetSession их и
// так не отдаёт, но без чистки таблица растёт вечно: при TTL в 30 дней там
// оседает каждый логин, что когда-либо был.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// ExpiredSessionCleaner — то, что умеет вычистить протухшее и сказать сколько.
type ExpiredSessionCleaner interface {
	DeleteExpired(ctx context.Context) (int64, error)
}

// StartSessionSweeper запускает фоновую чистку: первый проход сразу (иначе
// после рестарта мусор ждёт целый интервал), дальше по тикеру. Останавливается
// вместе с контекстом. Неположительный интервал — выключено: time.NewTicker на
// таком паникует, а ронять процесс из-за кривой переменной окружения незачем.
func StartSessionSweeper(ctx context.Context, cleaner ExpiredSessionCleaner,
	interval time.Duration) {
	if interval <= 0 {
		log.Warn().Dur("interval", interval).
			Msg("session sweeper disabled: interval must be positive")
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			sweepSessions(ctx, cleaner)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// sweepSessions делает один проход. Ошибка логируется и проглатывается:
// моргнувшая БД не должна убивать периодическую задачу до конца жизни процесса.
func sweepSessions(ctx context.Context, cleaner ExpiredSessionCleaner) {
	removed, err := cleaner.DeleteExpired(ctx)
	if err != nil {
		log.Error().Err(err).Msg("expired session sweep failed")
		return
	}
	if removed > 0 {
		log.Info().Int64("removed", removed).Msg("expired sessions swept")
	}
}
