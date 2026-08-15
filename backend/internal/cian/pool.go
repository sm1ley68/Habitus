package cian

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SearchSession is the single-proxy browser session boundary used by Pool.
type SearchSession interface {
	Search(context.Context, Filter, int) ([]Listing, error)
	Close()
}

type SessionFactory func(proxyURL string) (SearchSession, error)

type PoolConfig struct {
	Proxies []string
	Retries int
	Factory SessionFactory
	Backoff Pause
}

// Pool rotates independent cookie sessions. On a block the affected session is
// discarded before another proxy is tried.
type Pool struct {
	config   PoolConfig
	sessions []SearchSession
	next     int
}

func NewPool(config PoolConfig) (*Pool, error) {
	if len(config.Proxies) == 0 {
		return nil, errors.New("at least one proxy slot is required")
	}
	if config.Factory == nil {
		return nil, errors.New("session factory is required")
	}
	if config.Retries < 0 {
		return nil, errors.New("retries cannot be negative")
	}
	if config.Backoff == nil {
		config.Backoff = func(context.Context) error { return nil }
	}

	pool := &Pool{config: config, sessions: make([]SearchSession, len(config.Proxies))}
	for index, proxyURL := range config.Proxies {
		session, err := config.Factory(proxyURL)
		if err != nil {
			pool.Close()
			return nil, fmt.Errorf("create proxy session %d: %w", index+1, err)
		}
		pool.sessions[index] = session
	}
	return pool, nil
}

func (pool *Pool) Fetch(ctx context.Context, filter Filter, page int) ([]Listing, error) {
	var lastErr error
	for attempt := 0; attempt <= pool.config.Retries; attempt++ {
		index := pool.next % len(pool.sessions)
		pool.next++
		offers, err := pool.sessions[index].Search(ctx, filter, page)
		if err == nil {
			return offers, nil
		}
		lastErr = err
		if !IsRetryable(err) {
			return nil, err
		}
		if resetErr := pool.reset(index); resetErr != nil {
			return nil, errors.Join(err, resetErr)
		}
		if attempt < pool.config.Retries {
			if err := pool.config.Backoff(ctx); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("Cian request failed after %d attempts: %w", pool.config.Retries+1, lastErr)
}

func (pool *Pool) reset(index int) error {
	pool.sessions[index].Close()
	session, err := pool.config.Factory(pool.config.Proxies[index])
	if err != nil {
		return fmt.Errorf("reset proxy session %d: %w", index+1, err)
	}
	pool.sessions[index] = session
	return nil
}

func (pool *Pool) Close() {
	for _, session := range pool.sessions {
		if session != nil {
			session.Close()
		}
	}
}

// FixedPause is useful both for backoff and for deterministic callers.
func FixedPause(duration time.Duration) Pause {
	return func(ctx context.Context) error {
		if duration <= 0 {
			return nil
		}
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}
}
