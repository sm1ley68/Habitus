package service

import (
	"testing"
	"time"
)

// --- срок жизни кэша досье (Task 7) --------------------------------------

func TestDossierFreshWithinTTLAndNoListingUpdate(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-2 * time.Hour)

	if !dossierFresh(&dossierAt, nil, 24, now) {
		t.Fatal("кэш моложе TTL и без обновления объекта должен считаться свежим")
	}
}

func TestDossierStaleWhenOlderThanTTL(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-25 * time.Hour) // TTL = 24ч

	if dossierFresh(&dossierAt, nil, 24, now) {
		t.Fatal("кэш старше TTL должен считаться протухшим")
	}
}

func TestDossierStaleWhenListingUpdatedAfterDossier(t *testing.T) {
	// Досье моложе TTL, но объект в listings обновился циклом сбора уже после
	// того, как досье было посчитано, — кэш обязан протухнуть.
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-1 * time.Hour)
	listingAt := now.Add(-30 * time.Minute) // новее dossierAt

	if dossierFresh(&dossierAt, &listingAt, 24, now) {
		t.Fatal("досье старше listings.updated_at должно считаться протухшим")
	}
}

func TestDossierFreshWhenListingOlderThanDossier(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	dossierAt := now.Add(-1 * time.Hour)
	listingAt := now.Add(-3 * time.Hour) // старше dossierAt — объект не менялся с момента расчёта

	if !dossierFresh(&dossierAt, &listingAt, 24, now) {
		t.Fatal("объект не обновлялся после расчёта досье — кэш должен остаться свежим")
	}
}

func TestDossierStaleWhenNoCacheYet(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	if dossierFresh(nil, nil, 24, now) {
		t.Fatal("без dossier_updated_at кэша ещё нет — не может быть свежим")
	}
}
