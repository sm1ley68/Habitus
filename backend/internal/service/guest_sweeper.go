// guest_sweeper.go — чистка брошенных гостей. Гостевой аккаунт заводится на
// каждого посетителя без регистрации, и без чистки users растёт линейно по
// трафику, утаскивая за собой чаты и результаты по каскаду.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StaleGuestCleaner — то, что умеет вычистить брошенных гостей старше срока.
type StaleGuestCleaner interface {
	DeleteStaleGuests(ctx context.Context, olderThan time.Duration) (int64, error)
}

// StartGuestSweeper: первый проход сразу, дальше по тикеру, остановка вместе
// с контекстом. Неположительный интервал — выключено (time.NewTicker на
// таком паникует, а ронять процесс из-за кривой переменной окружения незачем).
func StartGuestSweeper(ctx context.Context, cleaner StaleGuestCleaner,
	interval, retention time.Duration) {
	if interval <= 0 {
		log.Warn().Dur("interval", interval).
			Msg("guest sweeper disabled: interval must be positive")
		return
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			sweepGuests(ctx, cleaner, retention)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sweepGuests(ctx context.Context, cleaner StaleGuestCleaner, retention time.Duration) {
	removed, err := cleaner.DeleteStaleGuests(ctx, retention)
	if err != nil {
		log.Error().Err(err).Msg("stale guest sweep failed")
		return
	}
	if removed > 0 {
		log.Info().Int64("removed", removed).Msg("stale guests swept")
	}
}
