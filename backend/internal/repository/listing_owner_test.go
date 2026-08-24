package repository

import (
	"context"
	"errors"
	"testing"
)

func TestSnapshotByExternalID(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, levels,
		                      geom, city, address, description, photos, owner_managed)
		VALUES ('cian_900001', 'cian', 12500000, 54.3, 2, 4, 17,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk',
		        'Москва, улица Мельникова, 3к1', 'Тихая двушка',
		        ARRAY['https://images.cdn-cian.ru/1.jpg'], false);`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	got, err := repo.SnapshotByExternalID(ctx, "cian_900001")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got.Address != "Москва, улица Мельникова, 3к1" || *got.Rooms != 2 {
		t.Fatalf("неверный снимок: %+v", got)
	}
	if got.Lng == nil || got.Lat == nil {
		t.Fatal("координаты должны быть разобраны")
	}
	if *got.Lng < 37.65 || *got.Lng > 37.67 || *got.Lat < 55.70 || *got.Lat > 55.72 {
		t.Fatalf("координаты разобраны неверно: %f %f", *got.Lng, *got.Lat)
	}
	if len(got.Photos) != 1 {
		t.Fatalf("фото не доехали: %+v", got.Photos)
	}
}

func TestSnapshotByExternalIDMissing(t *testing.T) {
	pool := testPool(t)
	repo := NewListingRepo(pool)
	if _, err := repo.SnapshotByExternalID(context.Background(), "cian_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидался ErrNotFound, получено %v", err)
	}
}

func TestFindSimilarMatchesNearbyTwin(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	// Близнец в 60 м, чужая квартира в том же доме и далёкая копия.
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, geom, city, address)
		VALUES ('cian_twin',    'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.66046, 55.7108), 4326), 'msk', 'Мельникова 3к1'),
		       ('cian_other',   'cian', 20000000, 88.0, 4, 9,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk', 'Мельникова 3к1'),
		       ('cian_faraway', 'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.50, 55.80), 4326), 'msk', 'Другой район');`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	rooms, level := 2, 4
	area := float32(54.3)
	lng, lat := 37.6595, 55.7108
	found, err := repo.FindSimilar(ctx, &lng, &lat, &rooms, &level, &area, "cian_new")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 1 || found[0].ExternalID != "cian_twin" {
		t.Fatalf("ожидался ровно близнец, получено %+v", found)
	}
}

func TestFindSimilarWithoutCoordinatesReturnsNothing(t *testing.T) {
	pool := testPool(t)
	repo := NewListingRepo(pool)
	rooms, level := 2, 4
	area := float32(54.0)

	// Объявление без геометрии не с чем сравнивать по расстоянию. Подставить
	// сюда ноль значило бы искать дубли в Гвинейском заливе.
	found, err := repo.FindSimilar(context.Background(), nil, nil, &rooms, &level, &area, "x")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("без координат сравнивать не с чем: %+v", found)
	}
}

func TestFindSimilarExcludesSelf(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `TRUNCATE listings CASCADE;`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO listings (external_id, source, price, area, rooms, level, geom, city, address)
		VALUES ('cian_self', 'cian', 12000000, 54.0, 2, 4,
		        ST_SetSRID(ST_MakePoint(37.6595, 55.7108), 4326), 'msk', 'Мельникова 3к1');`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	repo := NewListingRepo(pool)
	rooms, level := 2, 4
	area := float32(54.0)
	lng, lat := 37.6595, 55.7108
	found, err := repo.FindSimilar(ctx, &lng, &lat, &rooms, &level, &area, "cian_self")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("объявление не должно находить само себя: %+v", found)
	}
}

func TestFindSimilarWithoutRoomsReturnsNothing(t *testing.T) {
	pool := testPool(t)
	repo := NewListingRepo(pool)
	area := float32(54.0)
	lng, lat := 37.6595, 55.7108
	found, err := repo.FindSimilar(context.Background(), &lng, &lat, nil, nil, &area, "x")
	if err != nil {
		t.Fatalf("find similar: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("без комнат и этажа сравнивать не с чем: %+v", found)
	}
}
