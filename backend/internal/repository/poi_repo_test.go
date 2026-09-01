// poi_repo_test.go — DB-backed проверка скоупа выборки POI. До этого запрос
// шёл только по kind: с наполнением `poi` двумя городами питерские точки
// приезжали на московскую карту (~7 тыс. лишних фич на вьюпорт).
package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestPOIRepoListByKindsScopesToCityAndBBox(t *testing.T) {
	pool := testPool(t)
	repo := NewPOIRepo(pool)
	ctx := context.Background()

	// Отдельные города-однодневки на прогон: тестовая база не сбрасывается
	// между запусками, а фильтр надо проверить на своих, а не чужих строках.
	msk := "test_msk_" + uuid.NewString()
	spb := "test_spb_" + uuid.NewString()
	kind := "metro"

	for _, p := range []struct {
		city string
		name string
		lon  float64
		lat  float64
	}{
		{msk, "В центре Москвы", 37.60, 55.75},
		{msk, "За вьюпортом", 38.50, 56.20},
		{spb, "Питерская", 30.31, 59.94},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO poi (osm_id, kind, name, city, geom)
			VALUES ($1, $2, $3, $4, ST_SetSRID(ST_MakePoint($5, $6), 4326))`,
			int64(uuid.New().ID()), kind, p.name, p.city, p.lon, p.lat); err != nil {
			t.Fatalf("вставка точки %q: %v", p.name, err)
		}
	}

	// Без bbox — весь город, но только он.
	all, err := repo.ListByKinds(ctx, []string{kind}, msk, nil)
	if err != nil {
		t.Fatalf("ListByKinds() error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListByKinds(%s) = %d точек; хотим 2 московские", msk, len(all))
	}
	for _, p := range all {
		if p.Name == "Питерская" {
			t.Fatal("в выдаче по msk оказалась точка другого города")
		}
	}

	// С bbox — только попавшие во вьюпорт.
	box := [4]float64{37.3, 55.55, 37.9, 55.95}
	inBox, err := repo.ListByKinds(ctx, []string{kind}, msk, &box)
	if err != nil {
		t.Fatalf("ListByKinds() с bbox error = %v", err)
	}
	if len(inBox) != 1 || inBox[0].Name != "В центре Москвы" {
		t.Fatalf("ListByKinds() с bbox = %#v; хотим одну точку в центре", inBox)
	}

	// Город без точек — пусто, а не «всё подряд».
	none, err := repo.ListByKinds(ctx, []string{kind}, "test_nowhere_"+uuid.NewString(), nil)
	if err != nil {
		t.Fatalf("ListByKinds() по пустому городу error = %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("ListByKinds() по городу без точек = %d; хотим 0", len(none))
	}
}
