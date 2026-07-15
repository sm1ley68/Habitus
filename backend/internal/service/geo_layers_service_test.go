package service

import (
	"context"
	"reflect"
	"testing"

	"habitus-backend/internal/domain"
)

type fakePOILister struct {
	kinds []string
	pois  []domain.POI
}

func (f *fakePOILister) ListByKinds(_ context.Context, kinds []string) ([]domain.POI, error) {
	f.kinds = append([]string(nil), kinds...)
	return f.pois, nil
}

func TestGeoLayersReturnsMetro(t *testing.T) {
	repo := &fakePOILister{pois: []domain.POI{
		{Kind: "metro", Name: "Тверская", Lon: 37.604, Lat: 55.765},
		{Kind: "school", Name: "Школа", Lon: 37.6, Lat: 55.7},
	}}
	svc := NewGeoLayersService(repo)

	got, err := svc.Layers(context.Background(), []string{"metro", "unknown"})
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	if !reflect.DeepEqual(repo.kinds, []string{"metro"}) {
		t.Fatalf("ListByKinds() kinds = %v; want [metro]", repo.kinds)
	}
	metro, ok := got["metro"]
	if !ok || len(metro.Features) != 1 {
		t.Fatalf("Layers()[metro] = %#v; want one feature", metro)
	}
	if metro.Features[0].Properties["kind"] != "metro" || metro.Features[0].Properties["name"] != "Тверская" {
		t.Fatalf("metro feature properties = %#v", metro.Features[0].Properties)
	}
	if _, exists := got["unknown"]; exists {
		t.Fatal("unknown layer must be silently omitted")
	}
}

func TestGeoLayersDropsUnknownWithoutQuery(t *testing.T) {
	repo := &fakePOILister{}
	svc := NewGeoLayersService(repo)
	got, err := svc.Layers(context.Background(), []string{"unknown"})
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	if len(got) != 0 || repo.kinds != nil {
		t.Fatalf("Layers() = %#v, queried kinds = %v; want empty and no query", got, repo.kinds)
	}
}
