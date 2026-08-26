package repository

import (
	"context"
	"testing"

	"habitus-backend/internal/domain"
)

func TestEventInsert(t *testing.T) {
	pool := testPool(t)
	users := NewUserRepo(pool)
	events := NewEventRepo(pool)
	ctx := context.Background()

	userID := newTestUser(t, users)
	externalID := newExternalID()

	if err := events.Insert(ctx, domain.ProductEvent{
		UserID: userID, IsGuest: true, Kind: "passport_opened",
		ExternalID: externalID, Props: map[string]any{"contact_kind": "lead"},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var kind, contactKind string
	var isGuest bool
	err := pool.QueryRow(ctx, `
		SELECT kind, is_guest, props->>'contact_kind'
		FROM product_events WHERE user_id = $1 AND external_id = $2`,
		userID, externalID).Scan(&kind, &isGuest, &contactKind)
	if err != nil {
		t.Fatalf("чтение: %v", err)
	}
	if kind != "passport_opened" || !isGuest || contactKind != "lead" {
		t.Fatalf("записано kind=%q is_guest=%v props.contact_kind=%q", kind, isGuest, contactKind)
	}
}

// Событие без пользователя — законное состояние (нулевой uuid означает
// «актор неизвестен»), и падать на нём нельзя: телеметрия не критична.
func TestEventInsertAllowsMissingUser(t *testing.T) {
	pool := testPool(t)
	events := NewEventRepo(pool)

	if err := events.Insert(context.Background(), domain.ProductEvent{
		Kind: "search_started",
	}); err != nil {
		t.Fatalf("Insert без пользователя: %v", err)
	}
}
