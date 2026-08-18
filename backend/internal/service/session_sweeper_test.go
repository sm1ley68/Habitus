package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type countingCleaner struct {
	calls atomic.Int64
	err   error
}

func (c *countingCleaner) DeleteExpired(context.Context) (int64, error) {
	c.calls.Add(1)
	return 0, c.err
}

func waitFor(t *testing.T, want int64, got func() int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got() >= want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("вызовов = %d; want >= %d", got(), want)
}

func TestSweeperCleansOnStartAndOnEveryTick(t *testing.T) {
	// Первый проход сразу на старте: иначе после рестарта шлюза мусор ждёт
	// целый интервал (по умолчанию 6 часов).
	cleaner := &countingCleaner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartSessionSweeper(ctx, cleaner, 10*time.Millisecond)

	waitFor(t, 3, cleaner.calls.Load)
}

func TestSweeperStopsWithContext(t *testing.T) {
	cleaner := &countingCleaner{}
	ctx, cancel := context.WithCancel(context.Background())

	StartSessionSweeper(ctx, cleaner, 5*time.Millisecond)
	waitFor(t, 1, cleaner.calls.Load)
	cancel()

	time.Sleep(20 * time.Millisecond)
	stopped := cleaner.calls.Load()
	time.Sleep(30 * time.Millisecond)

	if got := cleaner.calls.Load(); got != stopped {
		t.Fatalf("вызовов после отмены = %d; было %d — горутина не остановилась", got, stopped)
	}
}

func TestSweeperKeepsRunningAfterFailure(t *testing.T) {
	// Упавшая чистка (БД моргнула) не должна убивать периодическую задачу.
	cleaner := &countingCleaner{err: errors.New("БД недоступна")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartSessionSweeper(ctx, cleaner, 5*time.Millisecond)

	waitFor(t, 3, cleaner.calls.Load)
}

func TestSweeperIgnoresNonPositiveInterval(t *testing.T) {
	// time.NewTicker паникует на неположительном интервале — кривая
	// переменная окружения не должна ронять процесс.
	cleaner := &countingCleaner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartSessionSweeper(ctx, cleaner, 0)

	time.Sleep(20 * time.Millisecond)
	if got := cleaner.calls.Load(); got != 0 {
		t.Fatalf("вызовов = %d; при нулевом интервале чистка не запускается", got)
	}
}
