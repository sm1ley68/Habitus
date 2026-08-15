package cian

import (
	"context"
	"errors"
	"testing"
)

type fakeSession struct {
	err    error
	offers []Listing
	closed bool
}

func (session *fakeSession) Search(context.Context, Filter, int) ([]Listing, error) {
	return session.offers, session.err
}
func (session *fakeSession) Close() { session.closed = true }

func TestPoolRotatesAfterCaptcha(t *testing.T) {
	t.Parallel()
	blocked := &fakeSession{err: ErrBlocked}
	working := &fakeSession{offers: []Listing{{CianID: "ok"}}}
	created := map[string]int{}
	factory := func(proxy string) (SearchSession, error) {
		created[proxy]++
		switch proxy {
		case "proxy-a":
			if created[proxy] == 1 {
				return blocked, nil
			}
			return &fakeSession{err: ErrBlocked}, nil
		case "proxy-b":
			return working, nil
		default:
			return nil, errors.New("unknown proxy")
		}
	}
	pool, err := NewPool(PoolConfig{Proxies: []string{"proxy-a", "proxy-b"}, Retries: 1, Factory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	offers, err := pool.Fetch(context.Background(), Filter{Room: 1}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(offers) != 1 || offers[0].CianID != "ok" {
		t.Fatalf("offers = %#v", offers)
	}
	if !blocked.closed || created["proxy-a"] != 2 {
		t.Fatalf("blocked session was not reset: closed=%v created=%v", blocked.closed, created)
	}
}
