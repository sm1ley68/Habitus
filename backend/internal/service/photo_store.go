package service

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
)

const publicPhotoPrefix = "/static/uploads/"

// extByContentType — единственный источник правды о допустимых форматах.
// Тип определяется по сигнатуре файла, а не по расширению и не по заголовку
// клиента: и то и другое подделывается тривиально.
var extByContentType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

// PhotoStore кладёт фотографии объявления под StaticDir шлюза. Отдельное
// хранилище (S3 и т.п.) не нужно: app.Static уже раздаёт этот каталог.
type PhotoStore struct {
	rootDir  string
	maxBytes int64
	maxCount int
}

func NewPhotoStore(rootDir string, maxBytes int64, maxCount int) *PhotoStore {
	return &PhotoStore{rootDir: rootDir, maxBytes: maxBytes, maxCount: maxCount}
}

func (s *PhotoStore) MaxCount() int { return s.maxCount }

// Save записывает файл и возвращает публичный URL. Имя, присланное клиентом,
// не используется нигде: путь строится из uuid объявления и нового uuid файла.
func (s *PhotoStore) Save(listingID uuid.UUID, _ string, r io.Reader, size int64) (string, error) {
	if size > s.maxBytes {
		return "", apperr.PhotoTooLarge(int(s.maxBytes >> 20))
	}

	// Читаем целиком с запасом в один байт: так превышение лимита ловится даже
	// когда клиент соврал в Content-Length.
	data, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > s.maxBytes {
		return "", apperr.PhotoTooLarge(int(s.maxBytes >> 20))
	}

	ext, ok := extByContentType[strings.Split(http.DetectContentType(data), ";")[0]]
	if !ok {
		return "", apperr.PhotoUnsupportedFormat()
	}

	dir := filepath.Join(s.rootDir, "uploads", listingID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := uuid.NewString() + ext
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		return "", err
	}
	return publicPhotoPrefix + listingID.String() + "/" + name, nil
}

// Delete удаляет наш файл. Ссылка на чужой CDN — не ошибка: у импортированного
// объявления фотографии остаются на стороне Циана, и удалять на диске нечего.
func (s *PhotoStore) Delete(listingID uuid.UUID, url string) error {
	if !strings.HasPrefix(url, publicPhotoPrefix) {
		return nil
	}
	rel := strings.TrimPrefix(url, publicPhotoPrefix)
	dir := filepath.Join(s.rootDir, "uploads", listingID.String())
	target := filepath.Join(s.rootDir, "uploads", filepath.Clean(rel))

	// filepath.Clean схлопывает ../, но результат всё равно надо проверить:
	// без этого «..» в имени выводит запись за пределы каталога объявления.
	if filepath.Dir(target) != dir {
		return errors.New("photo path escapes listing directory")
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *PhotoStore) DeleteAll(listingID uuid.UUID) error {
	return os.RemoveAll(filepath.Join(s.rootDir, "uploads", listingID.String()))
}
