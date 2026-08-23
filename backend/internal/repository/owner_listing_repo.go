package repository

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"habitus-backend/internal/domain"
)

// ErrExternalIDTaken — объявление с таким external_id уже привязано к кабинету
// (своему или чужому). Сервис превращает его в 409 listing_claimed_by_other.
var ErrExternalIDTaken = errors.New("owner listing external_id already taken")

// ownerListingColumns перечисляет колонки в том же порядке, в каком их читает
// scanOwnerListing. Держать эти два списка синхронными — единственное
// требование к любому новому запросу в этом файле.
const ownerListingColumns = `id, user_id, external_id, origin, status, verification, city,
	price, area, kitchen_area, rooms, level, levels,
	address, lng, lat,
	window_orientation, description, photos,
	source_url, import_error, created_at, updated_at, published_at`

type OwnerListingRepo struct {
	pool *pgxpool.Pool
}

func NewOwnerListingRepo(pool *pgxpool.Pool) *OwnerListingRepo {
	return &OwnerListingRepo{pool: pool}
}

func scanOwnerListing(row pgx.Row) (domain.OwnerListing, error) {
	var l domain.OwnerListing
	err := row.Scan(
		&l.ID, &l.UserID, &l.ExternalID, &l.Origin, &l.Status, &l.Verification, &l.City,
		&l.Price, &l.Area, &l.KitchenArea, &l.Rooms, &l.Level, &l.Levels,
		&l.Address, &l.Lng, &l.Lat, &l.WindowOrientation, &l.Description, &l.Photos,
		&l.SourceURL, &l.ImportError, &l.CreatedAt, &l.UpdatedAt, &l.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OwnerListing{}, ErrNotFound
	}
	return l, err
}

func (r *OwnerListingRepo) Create(ctx context.Context, l domain.OwnerListing) (domain.OwnerListing, error) {
	// window_orientation и photos — NOT NULL DEFAULT '{}', но explicit INSERT с
	// nil-слайсом шлёт настоящий SQL NULL, а не «колонка пропущена»: DEFAULT в
	// этом случае не подставляется. Нулевое значение domain.OwnerListing (nil
	// slice) должно лечь как пустой массив, а не уронить NOT NULL.
	windowOrientation := l.WindowOrientation
	if windowOrientation == nil {
		windowOrientation = []string{}
	}
	photos := l.Photos
	if photos == nil {
		photos = []string{}
	}
	created, err := scanOwnerListing(r.pool.QueryRow(ctx, `
		INSERT INTO owner_listings
			(user_id, external_id, origin, city, price, area, kitchen_area,
			 rooms, level, levels, address, lng, lat, window_orientation,
			 description, photos, source_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		RETURNING `+ownerListingColumns,
		l.UserID, l.ExternalID, l.Origin, l.City, l.Price, l.Area, l.KitchenArea,
		l.Rooms, l.Level, l.Levels, l.Address, l.Lng, l.Lat, windowOrientation,
		l.Description, photos, l.SourceURL))
	var pgErr *pgconn.PgError
	// Сужаем по имени ограничения, а не только по коду 23505: этот код общий для
	// любого уникального индекса таблицы, и первый же новый уникальный индекс
	// начал бы молча приезжать наружу как «ссылка занята другим аккаунтом».
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "owner_listings_external_id_key" {
		return domain.OwnerListing{}, ErrExternalIDTaken
	}
	return created, err
}

func (r *OwnerListingRepo) GetOwned(ctx context.Context, id, userID uuid.UUID) (domain.OwnerListing, error) {
	return scanOwnerListing(r.pool.QueryRow(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE id = $1 AND user_id = $2`, id, userID))
}

func (r *OwnerListingRepo) GetByExternalID(ctx context.Context, externalID string) (domain.OwnerListing, error) {
	return scanOwnerListing(r.pool.QueryRow(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE external_id = $1`, externalID))
}

func (r *OwnerListingRepo) List(ctx context.Context, userID uuid.UUID) ([]domain.OwnerListing, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+ownerListingColumns+` FROM owner_listings WHERE user_id = $1 ORDER BY updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []domain.OwnerListing{}
	for rows.Next() {
		l, err := scanOwnerListing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// UpdateFields применяет только переданные поля: COALESCE($n, колонка)
// оставляет прежнее значение там, где в запросе был nil.
//
// Явные приведения типов внутри COALESCE (::bigint, ::real, ::text[], ...)
// обязательны для параметров с типизированной nil-веткой: когда указатель в
// domain.OwnerListingFields равен nil, в драйвер уходит нетипизированный nil,
// и pgx не может вывести тип аргумента (особенно для колонки text[] —
// window_orientation) без явной подсказки. Без приведения запрос падает с
// «could not determine data type of parameter». Приведения безвредны и
// тогда, когда вывод типа сработал бы сам.
func (r *OwnerListingRepo) UpdateFields(ctx context.Context, id, userID uuid.UUID, f domain.OwnerListingFields) (domain.OwnerListing, error) {
	// f.WindowOrientation указывает на переданный срез. Если сам срез nil
	// (вызывающий хотел очистить window_orientation до пустого, а не оставить
	// прежним), *f.WindowOrientation тоже nil — тот же класс проблемы, что в
	// Create и SetPhotos: нетипизированный nil лёг бы как SQL NULL, и COALESCE
	// молча сохранил бы старое значение вместо очистки. Нормализуем в пустой срез.
	var wo any
	if f.WindowOrientation != nil {
		orientation := *f.WindowOrientation
		if orientation == nil {
			orientation = []string{}
		}
		wo = orientation
	}
	return scanOwnerListing(r.pool.QueryRow(ctx, `
		UPDATE owner_listings SET
			price = COALESCE($3::bigint, price),
			area = COALESCE($4::real, area),
			kitchen_area = COALESCE($5::real, kitchen_area),
			rooms = COALESCE($6::integer, rooms),
			level = COALESCE($7::integer, level),
			levels = COALESCE($8::integer, levels),
			address = COALESCE($9::text, address),
			lng = COALESCE($10::double precision, lng),
			lat = COALESCE($11::double precision, lat),
			window_orientation = COALESCE($12::text[], window_orientation),
			description = COALESCE($13::text, description),
			city = COALESCE($14::text, city),
			updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+ownerListingColumns,
		id, userID, f.Price, f.Area, f.KitchenArea, f.Rooms, f.Level, f.Levels,
		f.Address, f.Lng, f.Lat, wo, f.Description, f.City))
}

// SetPhotos перезаписывает список фото целиком. photos — NOT NULL DEFAULT '{}':
// nil (например, «удалили последнюю фотографию») лёг бы как явный SQL NULL и
// уронил бы запрос, поэтому здесь та же подстраховка, что и в Create.
func (r *OwnerListingRepo) SetPhotos(ctx context.Context, id, userID uuid.UUID, photos []string) (domain.OwnerListing, error) {
	if photos == nil {
		photos = []string{}
	}
	return scanOwnerListing(r.pool.QueryRow(ctx, `
		UPDATE owner_listings SET photos = $3, updated_at = now()
		WHERE id = $1 AND user_id = $2
		RETURNING `+ownerListingColumns, id, userID, photos))
}

// SetStatus — переход статусной машины. published_at ставится один раз, при
// первом переходе в published: это дата появления в витрине, а не последней правки.
func (r *OwnerListingRepo) SetStatus(ctx context.Context, id uuid.UUID, status, importError string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE owner_listings SET
			status = $2,
			import_error = $3,
			published_at = CASE WHEN $2 = 'published' AND published_at IS NULL
			                    THEN now() ELSE published_at END,
			updated_at = now()
		WHERE id = $1`, id, status, importError)
	if err != nil {
		return err
	}
	// Тот же контракт, что у Delete: несуществующий или уже удалённый id не
	// должен молча читаться как «переход состоялся».
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *OwnerListingRepo) Delete(ctx context.Context, id, userID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM owner_listings WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
