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
	svc := NewGeoLayersService(repo, &fakeEvidenceLister{}, &fakeListingLister{}, nil)

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
	svc := NewGeoLayersService(repo, &fakeEvidenceLister{}, &fakeListingLister{}, nil)
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
	svc := NewGeoLayersService(&fakePOILister{}, ev, &fakeListingLister{}, nil)
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
	svc := NewGeoLayersService(&fakePOILister{}, ev, &fakeListingLister{}, nil)
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
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{rows: rows}, &fakeListingLister{}, nil)
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

type fakeListingLister struct {
	rows   []domain.Listing
	bbox   [4]float64
	city   string
	limit  int
	called bool
}

func (f *fakeListingLister) ListInBBox(_ context.Context, city string, bbox [4]float64,
	limit int) ([]domain.Listing, error) {
	f.called, f.city, f.bbox, f.limit = true, city, bbox, limit
	return f.rows, nil
}

func TestListingsLayerRequiresBbox(t *testing.T) {
	lister := &fakeListingLister{}
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{}, lister, nil)
	fc, err := svc.Listings(context.Background(), "msk", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.Features) != 0 {
		t.Fatal("без вьюпорта отдавать нечего — 2143 объекта в браузер не грузим")
	}
	if lister.called {
		t.Fatal("без bbox репозиторий не должен опрашиваться")
	}
}

func TestListingsLayerCarriesCardFields(t *testing.T) {
	price := int64(21_300_000)
	rooms, area := 2, 40.0
	lon, lat := 37.62, 55.75
	lister := &fakeListingLister{rows: []domain.Listing{{
		ExternalID: "cian_1", Price: &price, Rooms: &rooms, Area: &area,
		Lon: &lon, Lat: &lat, Address: strp("Москва, Снежная улица, 4"),
		Photos: []string{"https://cdn/a.jpg"},
	}}}
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{}, lister, nil)
	box := [4]float64{37.5, 55.6, 37.7, 55.8}
	fc, err := svc.Listings(context.Background(), "msk", &box)
	if err != nil {
		t.Fatal(err)
	}
	if lister.bbox != box || lister.city != "msk" || lister.limit != listingsLimit {
		t.Fatalf("репозиторий вызван неверно: %#v", lister)
	}
	if len(fc.Features) != 1 {
		t.Fatalf("ожидалась одна точка, получено %#v", fc.Features)
	}
	f := fc.Features[0]
	if f.Geometry.Type != "Point" {
		t.Fatalf("геометрия должна быть точкой: %#v", f.Geometry)
	}
	for key, want := range map[string]any{
		"id": "cian_1", "address": "Москва, Снежная улица, 4",
		"cover_image": "https://cdn/a.jpg", "rooms": 2,
	} {
		if f.Properties[key] != want {
			t.Fatalf("properties[%q] = %#v; want %#v", key, f.Properties[key], want)
		}
	}
}

func TestListingsLayerSkipsRowsWithoutCoordinates(t *testing.T) {
	lister := &fakeListingLister{rows: []domain.Listing{{ExternalID: "no_geo"}}}
	svc := NewGeoLayersService(&fakePOILister{}, &fakeEvidenceLister{}, lister, nil)
	box := [4]float64{37.5, 55.6, 37.7, 55.8}
	fc, _ := svc.Listings(context.Background(), "msk", &box)
	if len(fc.Features) != 0 {
		t.Fatal("объект без координат на карту попасть не может")
	}
}

type fakeMetroLister struct {
	lines []domain.MetroLine
	city  string
}

func (f *fakeMetroLister) ListLines(_ context.Context, city string) ([]domain.MetroLine, error) {
	f.city = city
	return f.lines, nil
}

func TestMetroLayerCarriesLinesWithSystemAndColour(t *testing.T) {
	pois := &fakePOILister{pois: []domain.POI{
		{Kind: "metro", Name: "Сокольники", Lon: 37.68, Lat: 55.79},
	}}
	metro := &fakeMetroLister{lines: []domain.MetroLine{{
		Ref: "D1", Name: "МЦД-1", System: "mcd", Colour: strp("#F6A800"),
		GeometryJSON: `{"type":"LineString","coordinates":[[37.5,55.7],[37.6,55.8]]}`,
	}}}
	svc := NewGeoLayersService(pois, &fakeEvidenceLister{}, &fakeListingLister{}, metro)

	got, _, err := svc.Layers(context.Background(), "msk", []string{"metro"}, nil)
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}

	var points, lines int
	for _, f := range got["metro"].Features {
		switch f.Geometry.Type {
		case "Point":
			points++
		case "LineString":
			lines++
			// палитра не зашивается на фронте — цвет и система едут в properties
			if f.Properties["system"] != "mcd" {
				t.Fatalf("система не доехала: %#v", f.Properties)
			}
			if f.Properties["colour"] != "#F6A800" {
				t.Fatalf("цвет не доехал: %#v", f.Properties)
			}
			// R79: координаты в PostGIS/GeoJSON — [lng, lat]. Пинуем на
			// конкретных числах (московские долгота ~37, широта ~55) —
			// перестановка местами сразу заметна, в отличие от структурной
			// проверки "это LineString".
			coords, ok := f.Geometry.Coordinates.([]interface{})
			if !ok || len(coords) != 2 {
				t.Fatalf("geometry.coordinates не распакованы: %#v", f.Geometry.Coordinates)
			}
			first, ok := coords[0].([]interface{})
			if !ok || len(first) != 2 {
				t.Fatalf("первая точка линии не распакована: %#v", coords[0])
			}
			lng, lngOK := first[0].(float64)
			lat, latOK := first[1].(float64)
			if !lngOK || !latOK || lng != 37.5 || lat != 55.7 {
				t.Fatalf("порядок координат должен быть [lng,lat] = [37.5,55.7], получено [%v,%v]",
					first[0], first[1])
			}
		}
	}
	if points != 1 || lines != 1 {
		t.Fatalf("ожидались точка и линия, получено %d и %d", points, lines)
	}
	if metro.city != "msk" {
		t.Fatalf("репозиторий линий должен быть опрошен по городу: %q", metro.city)
	}
}

// R80a: NULL colour должен доехать до фронта явным null в properties, а не
// пустой строкой — пустая строка была бы синтетическим значением вместо
// отсутствующего замера.
func TestMetroLayerKeepsMissingColourNullNotEmpty(t *testing.T) {
	pois := &fakePOILister{pois: []domain.POI{
		{Kind: "metro", Name: "Партизанская", Lon: 37.75, Lat: 55.79},
	}}
	metro := &fakeMetroLister{lines: []domain.MetroLine{{
		Ref: "3", Name: "Арбатско-Покровская", System: "subway", Colour: nil,
		GeometryJSON: `{"type":"LineString","coordinates":[[37.5,55.7],[37.6,55.8]]}`,
	}}}
	svc := NewGeoLayersService(pois, &fakeEvidenceLister{}, &fakeListingLister{}, metro)

	got, _, err := svc.Layers(context.Background(), "msk", []string{"metro"}, nil)
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	for _, f := range got["metro"].Features {
		if f.Geometry.Type != "LineString" {
			continue
		}
		if f.Properties["colour"] != nil {
			t.Fatalf("отсутствующий цвет должен остаться nil, а не %#v", f.Properties["colour"])
		}
		if _, exists := f.Properties["colour"]; !exists {
			t.Fatal("ключ colour должен присутствовать явным null, а не пропадать")
		}
	}
}

// R80b: раньше эта проверка смотрела только внутрь got["parks"] и оставалась
// зелёной даже если удаление проверки «запрошен ли metro» протекло бы
// незапрошенным ключом metro в общий ответ — она проверяла не тот инвариант.
// Настоящий контракт — ключа "metro" не должно быть в ответе вовсе, если
// слой не запрашивался.
func TestOtherLayersHaveNoMetroLines(t *testing.T) {
	pois := &fakePOILister{pois: []domain.POI{
		{Kind: "park", Name: "Сокольники", Lon: 37.68, Lat: 55.79},
	}}
	metro := &fakeMetroLister{lines: []domain.MetroLine{{Ref: "1", System: "subway",
		GeometryJSON: `{"type":"LineString","coordinates":[[37.5,55.7],[37.6,55.8]]}`}}}
	svc := NewGeoLayersService(pois, &fakeEvidenceLister{}, &fakeListingLister{}, metro)

	got, _, err := svc.Layers(context.Background(), "msk", []string{"parks"}, nil)
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	if _, exists := got["metro"]; exists {
		t.Fatalf("metro не запрашивался — ключ не должен появляться в ответе вовсе: %#v", got["metro"])
	}
	for _, f := range got["parks"].Features {
		if f.Geometry.Type == "LineString" {
			t.Fatal("линии метро протекли в слой парков")
		}
	}
}

// R78: раньше эта проверка кормила сервис ПУСТЫМ списком линий (lines: nil),
// поэтому «нет LineString-фич» было верно по построению и не проверяло
// ничего — репозиторий вообще не был в игре. Здесь фейк-репозиторий
// сознательно нарушает контракт и возвращает линию с пустой GeometryJSON
// (данные линии есть, геометрии нет), проверяя, что СЕРВИС не доверяет
// репозиторию слепо и не превращает это в null-geometry фичу на карте.
func TestMetroLayerSkipsLineWithEmptyGeometry(t *testing.T) {
	pois := &fakePOILister{pois: []domain.POI{
		{Kind: "metro", Name: "Сокольники", Lon: 37.68, Lat: 55.79},
	}}
	metro := &fakeMetroLister{lines: []domain.MetroLine{
		{Ref: "1", Name: "Сокольническая", System: "subway", GeometryJSON: ""},
	}}
	svc := NewGeoLayersService(pois, &fakeEvidenceLister{}, &fakeListingLister{}, metro)

	got, _, err := svc.Layers(context.Background(), "msk", []string{"metro"}, nil)
	if err != nil {
		t.Fatalf("Layers() error = %v", err)
	}
	if len(got["metro"].Features) != 1 {
		t.Fatalf("линия без геометрии не должна порождать фичу: %#v", got["metro"].Features)
	}
	if got["metro"].Features[0].Geometry.Type != "Point" {
		t.Fatalf("осталась не станция-точка: %#v", got["metro"].Features[0])
	}
}
