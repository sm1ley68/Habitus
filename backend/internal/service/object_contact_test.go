package service

import (
	"testing"

	"habitus-backend/internal/domain"
)

// strp — существующий хелпер пакета (display_fields_test.go), свой не заводим.

// Объявление ведёт продавец в кабинете и оно опубликовано — значит есть кому
// перезвонить, показываем форму заявки.
func TestContactIsLeadForPublishedOwnerListing(t *testing.T) {
	got := BuildPassportContact(
		domain.OwnerListing{Status: "published"}, true,
		domain.Listing{SourceURL: strp("https://www.cian.ru/sale/flat/1/")})

	if got.Kind != ContactKindLead {
		t.Fatalf("kind = %q, ожидался lead", got.Kind)
	}
	// Ссылка на источник в режиме заявки не отдаётся: увести покупателя на
	// Циан мимо продавца, который завёл объявление здесь, — прямой вред.
	if got.SourceURL != "" {
		t.Fatalf("source_url = %q, при заявке его быть не должно", got.SourceURL)
	}
}

// Черновик и снятое с публикации заявки не принимают: продавец сам их скрыл.
func TestContactIsNotLeadForUnpublishedOwnerListing(t *testing.T) {
	for _, status := range []string{"draft", "publishing", "unpublished", "failed"} {
		got := BuildPassportContact(domain.OwnerListing{Status: status}, true, domain.Listing{})
		if got.Kind == ContactKindLead {
			t.Fatalf("статус %q принимает заявки, а не должен", status)
		}
	}
}

func TestContactIsExternalForShowcaseListing(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false,
		domain.Listing{SourceURL: strp("https://www.cian.ru/sale/flat/318394906/")})

	if got.Kind != ContactKindExternal {
		t.Fatalf("kind = %q, ожидался external", got.Kind)
	}
	if got.SourceURL != "https://www.cian.ru/sale/flat/318394906/" {
		t.Fatalf("source_url = %q", got.SourceURL)
	}
}

// Ни продавца, ни ссылки — честное «связаться нечем». Выдуманная кнопка тут
// хуже отсутствия кнопки: она ведёт в никуда.
func TestContactIsNoneWithoutSellerOrSource(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false, domain.Listing{})

	if got.Kind != ContactKindNone {
		t.Fatalf("kind = %q, ожидался none", got.Kind)
	}
	if got.SourceURL != "" {
		t.Fatalf("source_url = %q, ожидалась пустая строка", got.SourceURL)
	}
}

// Пустой source_url в витрине — не ссылка, а отсутствие ссылки.
func TestContactIsNoneOnEmptySourceURL(t *testing.T) {
	got := BuildPassportContact(domain.OwnerListing{}, false,
		domain.Listing{SourceURL: strp("")})

	if got.Kind != ContactKindNone {
		t.Fatalf("kind = %q, ожидался none", got.Kind)
	}
}
