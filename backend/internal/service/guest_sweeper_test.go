package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeGuestCleaner struct {
	mu      sync.Mutex
	calls   int
	lastAge time.Duration
	err     error
	sweeped chan struct{}
}

func (f *fakeGuestCleaner) DeleteStaleGuests(_ context.Context, olderThan time.Duration) (int64, error) {
	f.mu.Lock()
	f.calls++
	f.lastAge = olderThan
	f.mu.Unlock()
	select {
	case f.sweeped <- struct{}{}:
	default:
	}
	return 1, f.err
}

// Первый проход обязан случиться сразу: после рестарта мусор не должен ждать
// целый интервал — тот же приём, что у StartSessionSweeper.
func TestGuestSweeperRunsImmediately(t *testing.T) {
	cleaner := &fakeGuestCleaner{sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, time.Hour, 30*24*time.Hour)

	select {
	case <-cleaner.sweeped:
	case <-time.After(2 * time.Second):
		t.Fatal("первый проход не случился")
	}
	cleaner.mu.Lock()
	defer cleaner.mu.Unlock()
	if cleaner.lastAge != 30*24*time.Hour {
		t.Fatalf("olderThan = %v, ожидалось 720h", cleaner.lastAge)
	}
}

// Моргнувшая БД не должна убивать периодическую задачу до конца жизни
// процесса — та же гарантия, что у sweepSessions.
func TestGuestSweeperSurvivesError(t *testing.T) {
	cleaner := &fakeGuestCleaner{err: errors.New("db down"), sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, 20*time.Millisecond, time.Hour)

	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-cleaner.sweeped:
		case <-deadline:
			t.Fatalf("после ошибки свипер сделал только %d проход(ов)", i)
		}
	}
}

// Неположительный интервал — выключено, а не паника time.NewTicker.
func TestGuestSweeperDisabledOnNonPositiveInterval(t *testing.T) {
	cleaner := &fakeGuestCleaner{sweeped: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartGuestSweeper(ctx, cleaner, 0, time.Hour)

	select {
	case <-cleaner.sweeped:
		t.Fatal("свипер запустился при нулевом интервале")
	case <-time.After(100 * time.Millisecond):
	}
}
