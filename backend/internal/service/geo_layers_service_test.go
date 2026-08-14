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
	svc := NewGeoLayersService(repo, &fakeEvidenceLister{})

	got, _, err := svc.Layers(context.Background(), "msk", []string{"metro", "unknown"}, nil)
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
	svc := NewGeoLayersService(repo, &fakeEvidenceLister{})
	got, _, err := svc.Layers(context.Background(), "msk", []string{"unknown"}, nil)
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	if len(got) != 0 || repo.kinds != nil {
		t.Fatalf("Layers() = %#v, queried kinds = %v; want empty and no query", got, repo.kinds)
	}
}

type fakeEvidenceLister struct {
	rows   []domain.EvidenceFeature
	called bool
	bbox   [4]float64
	layers []string
	limit  int
}

func (f *fakeEvidenceLister) ListByLayers(_ context.Context, _ string, layers []string,
	bbox [4]float64, limit int) ([]domain.EvidenceFeature, error) {
	f.called = true
	f.layers = append([]string(nil), layers...)
	f.bbox = bbox
	f.limit = limit
	return f.rows, nil
}

func TestEvidenceLayerRequiresBbox(t *testing.T) {
	ev := &fakeEvidenceLister{}
	svc := NewGeoLayersService(&fakePOILister{}, ev)
	out, truncated, err := svc.Layers(context.Background(), "msk", []string{"communal"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["communal"].Features) != 0 {
		t.Fatal("без bbox evidence-слой должен быть пустым, а не 500")
	}
	if truncated["communal"] {
		t.Fatal("пустой слой не усечён")
	}
	if ev.called {
		t.Fatal("без bbox репозиторий не должен опрашиваться вовсе")
	}
}

func TestEvidenceLayerCarriesGeometryAndSource(t *testing.T) {
	w := 0.42
	ev := &fakeEvidenceLister{rows: []domain.EvidenceFeature{{
		Layer: "communal", Source: "reformagkh", Weight: &w,
		GeometryJSON: `{"type":"Point","coordinates":[37.6,55.7]}`,
	}}}
	svc := NewGeoLayersService(&fakePOILister{}, ev)
	box := [4]float64{37.5, 55.6, 37.7, 55.8}
	out, truncated, err := svc.Layers(context.Background(), "msk", []string{"communal"}, &box)
	if err != nil {
		t.Fatal(err)
	}
	if ev.bbox != box || !reflect.DeepEqual(ev.layers, []string{"communal"}) {
		t.Fatalf("репозиторий вызван с bbox=%v layers=%v", ev.bbox, ev.layers)
	}
	fc := out["communal"]
	if len(fc.Features) != 1 {
		t.Fatalf("ожидалась одна фича, получено %#v", fc.Features)
	}
	f := fc.Features[0]
	if f.Geometry.Type != "Point" {
		t.Fatalf("геометрия не распакована: %#v", f.Geometry)
	}
	if f.Properties["source"] != "reformagkh" {
		t.Fatalf("источник не доехал: %#v", f.Properties)
	}
	if f.Properties["weight"] != 0.42 {
		t.Fatalf("weight не доехал: %#v", f.Properties)
	}
	if truncated["communal"] {
		t.Fatal("одна фича — не усечение")
	}
}

func TestEvidenceLayerMarksTruncation(t *testing.T) {
	rows := make([]domain.EvidenceFeature, evidenceFeatureLimit+1)
	for i := range rows {
		rows[i] = domain.EvidenceFeature{Layer: "noise", Source: "osm",
			GeometryJSON: `{"type":"Point","coordinates":[37.6,55.7]}`}
	}
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{rows: rows})
	box := [4]float64{37.5, 55.6, 37.7, 55.8}
	out, truncated, err := svc.Layers(context.Background(), "msk", []string{"noise"}, &box)
	if err != nil {
		t.Fatal(err)
	}
	if len(out["noise"].Features) != evidenceFeatureLimit {
		t.Fatalf("слой должен быть срезан до %d, получено %d", evidenceFeatureLimit,
			len(out["noise"].Features))
	}
	if !truncated["noise"] {
		t.Fatal("усечение должно быть помечено")
	}
}

func TestEcologyIsGoneAndCrimeIsAllowed(t *testing.T) {
	if AllowedLayers["ecology"] {
		t.Fatal("под ecology нет источника нигде — слой должен быть убран")
	}
	if !AllowedLayers["crime"] || !AllowedLayers["metro"] {
		t.Fatal("crime и metro должны быть разрешены")
	}
}
