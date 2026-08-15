package cian

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreCSVUpsertSurvivesReopen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nested", "listings.csv")
	store, err := OpenStore(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	price := int64(10)
	stationTime := 5
	store.Merge([]Listing{{
		CianID: "1", Description: "old", Price: &price,
		Metro: []Metro{{Name: "Тверская", Time: &stationTime}}, CollectedAt: time.Unix(1, 0),
	}})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenStore(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	inserted, updated := reopened.Merge([]Listing{{CianID: "1", Description: "new", Metro: []Metro{}, CollectedAt: time.Unix(2, 0)}})
	if inserted != 0 || updated != 1 || reopened.Len() != 1 {
		t.Fatalf("inserted=%d updated=%d len=%d", inserted, updated, reopened.Len())
	}
	if err := reopened.Save(); err != nil {
		t.Fatal(err)
	}
	final, err := OpenStore(path, FormatCSV)
	if err != nil {
		t.Fatal(err)
	}
	if final.items[0].Description != "new" || final.items[0].Price != nil {
		t.Fatalf("final item = %#v", final.items[0])
	}
}

func TestStoreJSONRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "listings.json")
	store, err := OpenStore(path, "auto")
	if err != nil {
		t.Fatal(err)
	}
	store.Merge([]Listing{{CianID: "2", Description: "json", Metro: []Metro{}, CollectedAt: time.Unix(1, 0)}})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Len() != 1 || reopened.items[0].CianID != "2" {
		t.Fatalf("items = %#v", reopened.items)
	}
}
