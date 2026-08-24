package cian

import (
	"errors"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
)

// ErrNotAnOfferURL — вход не похож на ссылку на объявление Циана.
// Сервис превращает его в 400 cian_url_invalid с человеческим текстом:
// продавец вставляет ссылку из адресной строки, и «invalid request body»
// ему ничего не объясняет.
var ErrNotAnOfferURL = errors.New("not a Cian offer URL")

// Хост проверяется отдельно от пути, а не одной регуляркой по всей строке:
// иначе `cian.ru.evil.example/sale/flat/1/` прошло бы как валидное.
var (
	offerHostRe = regexp.MustCompile(`^(?:https?://)?(?:[a-z0-9-]+\.)*cian\.ru$`)
	offerPathRe = regexp.MustCompile(`^/(?:deal/)?(?:sale|rent)/flat/(\d{1,15})/?$`)
	bareIDRe    = regexp.MustCompile(`^\d{1,15}$`)
)

// ParseOfferURL достаёт числовой id объявления из того, что продавец вставил
// в поле: полной ссылки в любой форме или голого id.
func ParseOfferURL(raw string) (int64, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, ErrNotAnOfferURL
	}

	if bareIDRe.MatchString(value) {
		return parsePositive(value)
	}

	withScheme := value
	if !strings.HasPrefix(withScheme, "http://") && !strings.HasPrefix(withScheme, "https://") {
		withScheme = "https://" + withScheme
	}
	parsed, err := neturl.Parse(withScheme)
	if err != nil {
		return 0, ErrNotAnOfferURL
	}
	if !offerHostRe.MatchString(strings.ToLower(parsed.Host)) {
		return 0, ErrNotAnOfferURL
	}
	match := offerPathRe.FindStringSubmatch(parsed.Path)
	if match == nil {
		return 0, ErrNotAnOfferURL
	}
	return parsePositive(match[1])
}

func parsePositive(digits string) (int64, error) {
	id, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrNotAnOfferURL
	}
	return id, nil
}
