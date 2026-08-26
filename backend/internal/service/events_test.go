package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"habitus-backend/internal/domain"
)

type fakeEventStore struct {
	mu      sync.Mutex
	got     []domain.ProductEvent
	err     error
	written chan struct{}
}

func newFakeEventStore(err error) *fakeEventStore {
	return &fakeEventStore{err: err, written: make(chan struct{}, 16)}
}

func (f *fakeEventStore) Insert(_ context.Context, e domain.ProductEvent) error {
	f.mu.Lock()
	f.got = append(f.got, e)
	f.mu.Unlock()
	select {
	case f.written <- struct{}{}:
	default:
	}
	return f.err
}

func (f *fakeEventStore) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.got)
}

func TestEventRecorderWritesAsynchronously(t *testing.T) {
	store := newFakeEventStore(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 8)
	rec.Start(ctx)

	rec.Record(domain.ProductEvent{Kind: EventLeadSent, ExternalID: "cian_1"})

	select {
	case <-store.written:
	case <-time.After(2 * time.Second):
		t.Fatal("событие не записалось")
	}
	if store.count() != 1 {
		t.Fatalf("записано %d событий, ожидалось 1", store.count())
	}
}

// Переполненный буфер теряет событие и НЕ блокирует вызывающего: телеметрия
// не имеет права задержать ответ пользователю.
func TestEventRecorderDoesNotBlockOnFullBuffer(t *testing.T) {
	blocked := make(chan struct{})
	store := &blockingEventStore{release: blocked}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 1)
	rec.Start(ctx)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Record заблокировался на полном буфере")
	}
	close(blocked)
}

// Ошибка записи не роняет воркер: моргнувшая БД не должна выключать
// телеметрию до конца жизни процесса.
func TestEventRecorderSurvivesStoreError(t *testing.T) {
	store := newFakeEventStore(errors.New("db down"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := NewEventRecorder(store, 8)
	rec.Start(ctx)

	rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
	rec.Record(domain.ProductEvent{Kind: EventPassportOpened})

	deadline := time.After(2 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-store.written:
		case <-deadline:
			t.Fatalf("после ошибки записано только %d событий", i)
		}
	}
}

// Нулевой рекордер — законное состояние (телеметрия выключена в тестах,
// собирающих сервисы напрямую): Record на нём не должен паниковать.
func TestNilEventRecorderIsSafe(t *testing.T) {
	var rec *EventRecorder
	rec.Record(domain.ProductEvent{Kind: EventSearchStarted})
}

type blockingEventStore struct {
	release chan struct{}
}

func (b *blockingEventStore) Insert(context.Context, domain.ProductEvent) error {
	<-b.release
	return nil
}
