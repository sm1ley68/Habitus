package service

import (
	"context"
	"errors"
	"sync"
	"time"

	rand "math/rand/v2"

	"habitus-backend/internal/cian"
)

// LazyOfferFetcher держит по одной сессии на прокси и создаёт их по требованию.
// Сессия обязана оставаться привязанной к своему прокси: Циан привязывает
// challenge-куки к IP, и смена прокси под живой сессией гарантирует блокировку.
type LazyOfferFetcher struct {
	mu       sync.Mutex
	proxies  []string
	region   int
	timeout  time.Duration
	sessions map[string]*cian.Session
}

func NewLazyOfferFetcher(proxies []string, region int, timeout time.Duration) *LazyOfferFetcher {
	return &LazyOfferFetcher{proxies: proxies, region: region, timeout: timeout,
		sessions: map[string]*cian.Session{}}
}

func (f *LazyOfferFetcher) FetchByID(ctx context.Context, offerID int64) (cian.Listing, error) {
	session, err := f.session()
	if err != nil {
		return cian.Listing{}, err
	}
	listing, err := session.FetchByID(ctx, offerID)
	if errors.Is(err, cian.ErrBlocked) {
		// Заблокированную сессию выбрасываем: следующий импорт поднимет новую,
		// с другим отпечатком браузера и, возможно, другим прокси.
		f.drop(session)
	}
	return listing, err
}

func (f *LazyOfferFetcher) session() (*cian.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	key := ""
	if len(f.proxies) > 0 {
		key = f.proxies[rand.IntN(len(f.proxies))]
	}
	if s, ok := f.sessions[key]; ok {
		return s, nil
	}
	s, err := cian.NewTLSSession(key, f.timeout, cian.SessionConfig{
		Region: f.region, BootstrapCookies: true,
	})
	if err != nil {
		return nil, err
	}
	f.sessions[key] = s
	return s, nil
}

func (f *LazyOfferFetcher) drop(target *cian.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, s := range f.sessions {
		if s == target {
			s.Close()
			delete(f.sessions, key)
			return
		}
	}
}
