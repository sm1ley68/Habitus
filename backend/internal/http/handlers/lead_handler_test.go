package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/domain"
)

func TestLeadDTOShape(t *testing.T) {
	listingID := uuid.New()
	dto := LeadDTO(domain.Lead{
		ID: uuid.New(), ListingID: &listingID, ExternalID: "cian_318394906",
		Address: "Москва, улица Мельникова, 3к1", Name: "Иван",
		Contact: "+7 999 000-00-00", Message: "В субботу?",
		CreatedAt: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC),
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "listing_id", "external_id", "address",
		"name", "contact", "message", "created_at"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
	// Идентификаторов сторон в ответе быть не должно: продавцу они не нужны,
	// а покупательский id — лишняя утечка из кабинета.
	for _, key := range []string{"seller_id", "buyer_id"} {
		if _, ok := got[key]; ok {
			t.Fatalf("в ответе есть лишнее поле %q", key)
		}
	}
}

// Объявление удалено — у заявки нет listing_id. Ответ обязан нести null, а не
// нулевой uuid: синтетическое значение вместо отсутствующего в проекте
// запрещено (CLAUDE.md).
func TestLeadDTOOrphanedListingIsNull(t *testing.T) {
	dto := LeadDTO(domain.Lead{
		ID: uuid.New(), ListingID: nil, ExternalID: "cian_318394906",
		Address: "Москва, улица Мельникова, 3к1", Name: "Иван",
		Contact:   "+7 999 000-00-00",
		CreatedAt: time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC),
	})

	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, ok := got["listing_id"]; !ok || v != nil {
		t.Fatalf("listing_id = %v, ожидался null", v)
	}
}
