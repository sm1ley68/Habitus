// partner_sweeper.go — чистка того, что Partner API накапливает по ходу
// работы: ключи идемпотентности и сохранённые поиски. Тот же приём, что у
// StartSessionSweeper и StartGuestSweeper — горутина с тикером, без
// внешнего планировщика.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

type idempotencySweeper interface {
	SweepIdempotency(ctx context.Context, ttl time.Duration) (int64, error)
}

type searchSweeper interface {
	SweepSearches(ctx context.Context, ttl time.Duration) (int64, error)
}

// StartPartnerSweeper. Первый проход делается сразу на старте: после долгого
// простоя мусор уже накоплен, и ждать целый интервал незачем.
func StartPartnerSweeper(ctx context.Context, keys idempotencySweeper, searches searchSweeper,
	every, idempotencyTTL, searchTTL time.Duration) {
	if every <= 0 {
		return
	}
	sweep := func() {
		if n, err := keys.SweepIdempotency(ctx, idempotencyTTL); err != nil {
			log.Error().Err(err).Msg("partner idempotency sweep failed")
		} else if n > 0 {
			log.Info().Int64("rows", n).Msg("partner idempotency keys swept")
		}
		if n, err := searches.SweepSearches(ctx, searchTTL); err != nil {
			log.Error().Err(err).Msg("partner search sweep failed")
		} else if n > 0 {
			log.Info().Int64("rows", n).Msg("partner searches swept")
		}
	}
	go func() {
		sweep()
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}
