package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestOwnerUpsertSendsPayloadAndReadsIndexed(t *testing.T) {
	var got OwnerUpsertRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/owner-upsert" {
			t.Errorf("путь = %q", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("тело не разбирается: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"external_id":"owner_x","indexed":true}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	price := int64(12_000_000)
	resp, err := c.OwnerUpsert(context.Background(), OwnerUpsertRequest{
		ExternalID: "owner_x", Source: "owner", City: "msk",
		Price: &price, Lng: 37.6, Lat: 55.7,
	})
	if err != nil {
		t.Fatalf("owner upsert: %v", err)
	}
	if !resp.Indexed {
		t.Fatal("indexed должен быть true")
	}
	if got.ExternalID != "owner_x" || got.City != "msk" || got.Lng != 37.6 {
		t.Fatalf("на ML ушло не то: %+v", got)
	}
}

func TestOwnerUpsertMapsValidationError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"detail":{"field":"coordinates","message":"Координаты вне границ выбранного города"}}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	_, err := c.OwnerUpsert(context.Background(), OwnerUpsertRequest{ExternalID: "owner_x", Source: "owner", City: "spb"})

	var invalid *OwnerListingInvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("ожидалась ошибка валидации, получено %v", err)
	}
	if invalid.Field != "coordinates" {
		t.Fatalf("поле = %q", invalid.Field)
	}
	if invalid.Message == "" {
		t.Fatal("сообщение должно доезжать до продавца")
	}
}

func TestOwnerWithdraw(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/listings/owner-withdraw" {
			t.Errorf("путь = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"external_id":"owner_x","deactivated":true}`))
	}))
	defer server.Close()

	c := NewMLClient(server.URL, 5*time.Second)
	resp, err := c.OwnerWithdraw(context.Background(), "owner_x")
	if err != nil {
		t.Fatalf("withdraw: %v", err)
	}
	if !resp.Deactivated {
		t.Fatal("deactivated должен быть true")
	}
}
