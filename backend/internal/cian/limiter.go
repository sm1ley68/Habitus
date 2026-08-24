package cian

import (
	"sync"
	"time"
)

// RateLimiter — общий потолок исходящих запросов к Циану, а не лимит на
// пользователя. Сколько бы человек ни импортировали одновременно, наружу
// уходит не больше perMinute запросов: бан прилетает по IP всему сервису
// сразу, поэтому ограничивать надо суммарный темп.
//
// Окно скользящее, отметки хранятся списком: perMinute — единицы, поэтому
// цена обхода списка ничтожна, а поведение точнее, чем у ведра с доливом.
type RateLimiter struct {
	mu        sync.Mutex
	perMinute int
	now       func() time.Time
	marks     []time.Time
}

func NewRateLimiter(perMinute int, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{perMinute: perMinute, now: now}
}

func (l *RateLimiter) Allow() bool {
	if l.perMinute <= 0 {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-time.Minute)
	fresh := l.marks[:0]
	for _, m := range l.marks {
		if m.After(cutoff) {
			fresh = append(fresh, m)
		}
	}
	l.marks = fresh
	if len(l.marks) >= l.perMinute {
		return false
	}
	l.marks = append(l.marks, l.now())
	return true
}

// UserQuota — скользящее часовое окно на пользователя. Защищает не Циан, а
// сервис: один человек не должен выбирать общий потолок целиком.
type UserQuota struct {
	mu      sync.Mutex
	perHour int
	now     func() time.Time
	marks   map[string][]time.Time
}

func NewUserQuota(perHour int, now func() time.Time) *UserQuota {
	if now == nil {
		now = time.Now
	}
	return &UserQuota{perHour: perHour, now: now, marks: map[string][]time.Time{}}
}

func (q *UserQuota) Allow(userID string) bool {
	if q.perHour <= 0 {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()

	cutoff := q.now().Add(-time.Hour)
	kept := q.marks[userID][:0]
	for _, m := range q.marks[userID] {
		if m.After(cutoff) {
			kept = append(kept, m)
		}
	}
	if len(kept) >= q.perHour {
		// Отметки чистим даже при отказе: иначе список растёт бесконечно.
		q.marks[userID] = kept
		return false
	}
	q.marks[userID] = append(kept, q.now())
	return true
}
