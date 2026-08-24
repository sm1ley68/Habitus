package service

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
)

// Минимальные валидные заголовки: http.DetectContentType смотрит только на них.
var (
	jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 600)...)
	pngBytes  = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{0}, 600)...)
	gifBytes  = append([]byte("GIF89a"), bytes.Repeat([]byte{0}, 600)...)
	textBytes = []byte(strings.Repeat("это не картинка ", 40))
)

func TestPhotoStoreSavesAndReturnsPublicURL(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()

	url, err := store.Save(listingID, "фото квартиры.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !strings.HasPrefix(url, "/static/uploads/"+listingID.String()+"/") {
		t.Fatalf("url = %q", url)
	}
	if !strings.HasSuffix(url, ".jpg") {
		t.Fatalf("расширение должно выводиться из содержимого: %q", url)
	}
	// Имя файла клиента не должно попадать в путь: это вектор обхода каталога
	// и источник кракозябр в URL.
	if strings.Contains(url, "фото") {
		t.Fatalf("имя клиента протекло в путь: %q", url)
	}
	onDisk := filepath.Join(root, "uploads", listingID.String())
	entries, err := os.ReadDir(onDisk)
	if err != nil || len(entries) != 1 {
		t.Fatalf("файл не сохранён: %v, %d", err, len(entries))
	}
}

func TestPhotoStoreAcceptsPNG(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)
	url, err := store.Save(uuid.New(), "x.bin", bytes.NewReader(pngBytes), int64(len(pngBytes)))
	if err != nil {
		t.Fatalf("save png: %v", err)
	}
	if !strings.HasSuffix(url, ".png") {
		t.Fatalf("url = %q", url)
	}
}

func TestPhotoStoreRejectsByContentNotExtension(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)

	// Текст, переименованный в .jpg, — самый частый способ протащить не-картинку.
	_, err := store.Save(uuid.New(), "trojan.jpg", bytes.NewReader(textBytes), int64(len(textBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_unsupported_format" {
		t.Fatalf("ожидался photo_unsupported_format, получено %v", err)
	}
}

func TestPhotoStoreRejectsGIF(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)
	_, err := store.Save(uuid.New(), "anim.gif", bytes.NewReader(gifBytes), int64(len(gifBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_unsupported_format" {
		t.Fatalf("ожидался photo_unsupported_format, получено %v", err)
	}
}

func TestPhotoStoreRejectsOversize(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 100, 20)
	_, err := store.Save(uuid.New(), "big.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "photo_too_large" {
		t.Fatalf("ожидался photo_too_large, получено %v", err)
	}
}

func TestPhotoStoreDeleteRemovesOnlyOwnFile(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()
	first, _ := store.Save(listingID, "a.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))
	second, _ := store.Save(listingID, "b.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	if err := store.Delete(listingID, first); err != nil {
		t.Fatalf("delete: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(root, "uploads", listingID.String()))
	if len(entries) != 1 {
		t.Fatalf("должен остаться один файл, осталось %d", len(entries))
	}
	if !strings.HasSuffix(second, entries[0].Name()) {
		t.Fatalf("удалён не тот файл")
	}
}

func TestPhotoStoreDeleteRefusesPathEscape(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()

	err := store.Delete(listingID, "/static/uploads/"+listingID.String()+"/../../../etc/passwd")
	if err == nil {
		t.Fatal("выход за пределы каталога объявления обязан отвергаться")
	}
}

func TestPhotoStoreDeleteIgnoresExternalURL(t *testing.T) {
	store := NewPhotoStore(t.TempDir(), 10<<20, 20)

	// Фото с CDN Циана не наши: удалять на диске нечего, и это не ошибка —
	// ссылка просто убирается из массива.
	if err := store.Delete(uuid.New(), "https://images.cdn-cian.ru/images/1.jpg"); err != nil {
		t.Fatalf("внешняя ссылка не должна быть ошибкой: %v", err)
	}
}

func TestPhotoStoreDeleteAll(t *testing.T) {
	root := t.TempDir()
	store := NewPhotoStore(root, 10<<20, 20)
	listingID := uuid.New()
	_, _ = store.Save(listingID, "a.jpg", bytes.NewReader(jpegBytes), int64(len(jpegBytes)))

	if err := store.DeleteAll(listingID); err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "uploads", listingID.String())); !os.IsNotExist(err) {
		t.Fatal("каталог объявления должен быть удалён")
	}
}
