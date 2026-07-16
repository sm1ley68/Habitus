package cian

import (
	"context"
	"testing"
)

type fakeFetcher struct {
	pages   map[int][]Listing
	visited []Filter
}

func (fetcher *fakeFetcher) Fetch(_ context.Context, filter Filter, page int) ([]Listing, error) {
	fetcher.visited = append(fetcher.visited, filter)
	return fetcher.pages[page], nil
}

func TestScraperDeduplicatesAndCheckpointsPages(t *testing.T) {
	t.Parallel()
	scraper, err := NewScraper(ScraperConfig{
		Filters:   []Filter{{Room: 1}},
		Pages:     5,
		MaxOffers: 3,
	}, &fakeFetcher{pages: map[int][]Listing{
		1: {{CianID: "1"}, {CianID: "2"}},
		2: {{CianID: "2"}, {CianID: "3"}, {CianID: "4"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var checkpoints [][]Listing
	progress, err := scraper.Run(context.Background(), func(items []Listing, _ Progress) error {
		checkpoints = append(checkpoints, items)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if progress.UniqueOffers != 3 || progress.CompletedPages != 2 {
		t.Fatalf("progress = %#v", progress)
	}
	if len(checkpoints) != 2 || len(checkpoints[0]) != 2 || len(checkpoints[1]) != 1 || checkpoints[1][0].CianID != "3" {
		t.Fatalf("checkpoints = %#v", checkpoints)
	}
}

func TestScraperVisitsFiltersRoundRobinByPage(t *testing.T) {
	t.Parallel()
	filters := []Filter{{Room: 1}, {Room: 2}, {Room: 3}, {Room: 4}}
	fetcher := &fakeFetcher{pages: map[int][]Listing{
		1: {{CianID: "same-page-id"}},
		2: {},
	}}
	scraper, err := NewScraper(ScraperConfig{Filters: filters, Pages: 2, MaxOffers: 10}, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scraper.Run(context.Background(), func([]Listing, Progress) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(fetcher.visited) != 8 {
		t.Fatalf("visited = %#v", fetcher.visited)
	}
	for index, filter := range filters {
		if fetcher.visited[index] != filter {
			t.Fatalf("first page order = %#v", fetcher.visited[:4])
		}
	}
}
