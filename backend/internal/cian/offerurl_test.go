package cian

import (
	"errors"
	"testing"
)

func TestParseOfferURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int64
	}{
		{"канонический sale", "https://www.cian.ru/sale/flat/318394906/", 318394906},
		{"хвост сессии", "https://www.cian.ru/sale/flat/317927888/?mlSearchSessionGuid=c532b9c1", 317927888},
		{"аренда", "https://www.cian.ru/rent/flat/302010101/", 302010101},
		{"форма deal", "https://www.cian.ru/deal/sale/flat/318394906/", 318394906},
		{"поддомен города", "https://spb.cian.ru/sale/flat/311111111/", 311111111},
		{"мобильный поддомен", "https://m.cian.ru/sale/flat/311111112/", 311111112},
		{"без схемы", "www.cian.ru/sale/flat/318394906/", 318394906},
		{"без www", "https://cian.ru/sale/flat/318394906", 318394906},
		{"голый id", "318394906", 318394906},
		{"пробелы вокруг", "  https://www.cian.ru/sale/flat/318394906/  ", 318394906},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseOfferURL(tc.in)
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if got != tc.want {
				t.Fatalf("получено %d, ожидалось %d", got, tc.want)
			}
		})
	}
}

func TestParseOfferURLRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"пусто", ""},
		{"чужой домен", "https://www.avito.ru/moskva/kvartiry/1234567"},
		{"домен-подделка", "https://cian.ru.evil.example/sale/flat/318394906/"},
		{"страница поиска", "https://www.cian.ru/cat.php?deal_type=sale"},
		{"жилой комплекс", "https://www.cian.ru/zhk/shift-12345/"},
		{"не число", "https://www.cian.ru/sale/flat/abcdef/"},
		{"просто текст", "моя квартира"},
		{"id нулевой", "0"},
		{"id слишком длинный", "12345678901234567890123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseOfferURL(tc.in); !errors.Is(err, ErrNotAnOfferURL) {
				t.Fatalf("ожидался ErrNotAnOfferURL, получено %v", err)
			}
		})
	}
}
