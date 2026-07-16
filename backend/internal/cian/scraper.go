package cian

import (
	"context"
	"errors"
	"fmt"
	rand "math/rand/v2"
	"time"
)

type PageFetcher interface {
	Fetch(context.Context, Filter, int) ([]Listing, error)
}

type ScraperConfig struct {
	Filters      []Filter
	Pages        int
	MaxOffers    int
	BetweenPages Pause
}

type Progress struct {
	Filter         Filter
	Page           int
	PageOffers     int
	UniqueOffers   int
	CompletedPages int
}

type PageSink func([]Listing, Progress) error

type Scraper struct {
	config  ScraperConfig
	fetcher PageFetcher
}

func NewScraper(config ScraperConfig, fetcher PageFetcher) (*Scraper, error) {
	if fetcher == nil {
		return nil, errors.New("page fetcher is required")
	}
	if len(config.Filters) == 0 {
		return nil, errors.New("at least one search filter is required")
	}
	if config.Pages < 1 {
		return nil, errors.New("pages must be at least 1")
	}
	if config.MaxOffers < 1 {
		return nil, errors.New("max offers must be at least 1")
	}
	if config.BetweenPages == nil {
		config.BetweenPages = func(context.Context) error { return nil }
	}
	return &Scraper{config: config, fetcher: fetcher}, nil
}

// Run walks active filters page-by-page in round-robin order. This prevents a
// max-offers limit from filling the entire dataset with the first room/price
// bucket. The sink is called after every non-empty page so callers can
// checkpoint results and preserve progress on interruption.
func (scraper *Scraper) Run(ctx context.Context, sink PageSink) (Progress, error) {
	if sink == nil {
		return Progress{}, errors.New("page sink is required")
	}
	seen := make(map[string]struct{}, scraper.config.MaxOffers)
	progress := Progress{}
	active := make([]bool, len(scraper.config.Filters))
	for index := range active {
		active[index] = true
	}

	for page := 1; page <= scraper.config.Pages; page++ {
		anyActive := false
		for filterIndex, filter := range scraper.config.Filters {
			if !active[filterIndex] {
				continue
			}
			anyActive = true
			if err := ctx.Err(); err != nil {
				return progress, err
			}
			offers, err := scraper.fetcher.Fetch(ctx, filter, page)
			if err != nil {
				return progress, fmt.Errorf("room=%d price=%d:%d page=%d: %w", filter.Room, filter.MinPrice, filter.MaxPrice, page, err)
			}
			progress = Progress{
				Filter:         filter,
				Page:           page,
				PageOffers:     len(offers),
				UniqueOffers:   len(seen),
				CompletedPages: progress.CompletedPages + 1,
			}
			if len(offers) == 0 {
				active[filterIndex] = false
				if err := scraper.config.BetweenPages(ctx); err != nil {
					return progress, err
				}
				continue
			}

			accepted := make([]Listing, 0, len(offers))
			for _, offer := range offers {
				if _, exists := seen[offer.CianID]; exists {
					continue
				}
				seen[offer.CianID] = struct{}{}
				accepted = append(accepted, offer)
				if len(seen) >= scraper.config.MaxOffers {
					break
				}
			}
			progress.UniqueOffers = len(seen)
			if err := sink(accepted, progress); err != nil {
				return progress, fmt.Errorf("checkpoint Cian results: %w", err)
			}
			if len(seen) >= scraper.config.MaxOffers {
				return progress, nil
			}
			if err := scraper.config.BetweenPages(ctx); err != nil {
				return progress, err
			}
		}
		if !anyActive {
			break
		}
	}
	return progress, nil
}

func RandomPause(minimum, maximum time.Duration) (Pause, error) {
	if minimum < 0 || maximum < 0 {
		return nil, errors.New("delays cannot be negative")
	}
	if maximum < minimum {
		return nil, errors.New("maximum delay is smaller than minimum delay")
	}
	return func(ctx context.Context) error {
		duration := minimum
		if spread := maximum - minimum; spread > 0 {
			duration += time.Duration(rand.Int64N(int64(spread) + 1))
		}
		return FixedPause(duration)(ctx)
	}, nil
}
