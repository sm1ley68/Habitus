// metro_repo_test.go — DB-backed проверка фильтра «нет геометрии» в самом
// SQL-запросе (R78: раньше это проверялось только чтением schema.sql и
// habitus/geo/metro.py, ни разу не исполнив запрос против реальной базы).
package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestMetroRepoListLinesSkipsNullGeometry — metro_line_geom.geom nullable
// (Задачи 5-6): строка линии может существовать, а геометрии под ней ещё
// нет. Такая линия не должна попасть в ответ ни пустой, ни нулевой
// LineString-фичей — она должна просто отсутствовать в срезе.
func TestMetroRepoListLinesSkipsNullGeometry(t *testing.T) {
	pool := testPool(t)
	repo := NewMetroRepo(pool)
	ctx := context.Background()
	// Уникальный city на прогон: testPool не сбрасывает персистентную
	// тестовую базу между запусками (см. newExternalID в main_test.go),
	// а UNIQUE(city, system, ref) не пустит повторный INSERT с тем же ref.
	city := "test_" + uuid.NewString()

	var withGeomID, withoutGeomID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO metro_line (city, system, ref, name, colour, headway_s, fallback_speed_kmh)
		VALUES ($1, 'mcd', 'D1', 'МЦД-1', '#F6A800', 300, 42.0) RETURNING id`,
		city).Scan(&withGeomID); err != nil {
		t.Fatalf("вставка линии с геометрией: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO metro_line (city, system, ref, name, colour, headway_s, fallback_speed_kmh)
		VALUES ($1, 'mcd', 'D2', 'МЦД-2', NULL, 300, 42.0) RETURNING id`,
		city).Scan(&withoutGeomID); err != nil {
		t.Fatalf("вставка линии без геометрии: %v", err)
	}

	// withGeomID получает настоящую геометрию; withoutGeomID — строку в
	// metro_line_geom со geom = NULL (линия заведена, геометрия ещё не
	// собрана — легитимное промежуточное состояние графа).
	if _, err := pool.Exec(ctx, `
		INSERT INTO metro_line_geom (line_id, geom)
		VALUES ($1, ST_GeomFromText('LINESTRING(37.5 55.7, 37.6 55.8)', 4326))`,
		withGeomID); err != nil {
		t.Fatalf("вставка геометрии: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO metro_line_geom (line_id, geom) VALUES ($1, NULL)`,
		withoutGeomID); err != nil {
		t.Fatalf("вставка строки с geom=NULL: %v", err)
	}

	lines, err := repo.ListLines(ctx, city)
	if err != nil {
		t.Fatalf("ListLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("ожидалась одна линия с геометрией, получено %d: %#v", len(lines), lines)
	}
	if lines[0].Ref != "D1" {
		t.Fatalf("сквозь фильтр прошла не та линия: %#v", lines[0])
	}
	if lines[0].Colour == nil || *lines[0].Colour != "#F6A800" {
		t.Fatalf("цвет не доехал: %#v", lines[0].Colour)
	}
}

// TestMetroRepoListLinesKeepsColourNull — R80a: NULL colour должен остаться
// nil, а не превратиться в пустую строку. Пустая строка была бы
// синтетическим значением вместо отсутствующего замера — тем же нарушением,
// какое этот проект запрещает для остальных полей (см. CLAUDE.md).
func TestMetroRepoListLinesKeepsColourNull(t *testing.T) {
	pool := testPool(t)
	repo := NewMetroRepo(pool)
	ctx := context.Background()
	city := "test_" + uuid.NewString()

	var lineID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO metro_line (city, system, ref, name, colour, headway_s, fallback_speed_kmh)
		VALUES ($1, 'subway', '1', 'Сокольническая', NULL, 120, 41.0) RETURNING id`,
		city).Scan(&lineID); err != nil {
		t.Fatalf("вставка линии: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO metro_line_geom (line_id, geom)
		VALUES ($1, ST_GeomFromText('LINESTRING(37.5 55.7, 37.6 55.8)', 4326))`,
		lineID); err != nil {
		t.Fatalf("вставка геометрии: %v", err)
	}

	lines, err := repo.ListLines(ctx, city)
	if err != nil {
		t.Fatalf("ListLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("ожидалась одна линия, получено %d", len(lines))
	}
	if lines[0].Colour != nil {
		t.Fatalf("NULL colour должен остаться nil, получено %q", *lines[0].Colour)
	}
}

// TestMetroRepoListLinesPreservesCoordinateOrder — R79: ST_AsGeoJSON должен
// сохранять порядок [lng, lat], как везде в проекте. Числа выбраны так,
// чтобы перестановка была невозможна перепутать: долгота Москвы ~37,
// широта ~55 — цифры разного порядка видны на глаз.
func TestMetroRepoListLinesPreservesCoordinateOrder(t *testing.T) {
	pool := testPool(t)
	repo := NewMetroRepo(pool)
	ctx := context.Background()
	city := "test_" + uuid.NewString()

	var lineID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO metro_line (city, system, ref, name, colour, headway_s, fallback_speed_kmh)
		VALUES ($1, 'subway', '1', 'Сокольническая', NULL, 120, 41.0) RETURNING id`,
		city).Scan(&lineID); err != nil {
		t.Fatalf("вставка линии: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO metro_line_geom (line_id, geom)
		VALUES ($1, ST_GeomFromText('LINESTRING(37.6176 55.7558, 37.53 55.70)', 4326))`,
		lineID); err != nil {
		t.Fatalf("вставка геометрии: %v", err)
	}

	lines, err := repo.ListLines(ctx, city)
	if err != nil {
		t.Fatalf("ListLines: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("ожидалась одна линия, получено %d", len(lines))
	}
	want := `{"type":"LineString","coordinates":[[37.6176,55.7558],[37.53,55.7]]}`
	if lines[0].GeometryJSON != want {
		t.Fatalf("порядок координат разошёлся: получено %s, ожидалось %s",
			lines[0].GeometryJSON, want)
	}
}
