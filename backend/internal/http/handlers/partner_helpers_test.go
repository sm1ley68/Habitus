package handlers

import "encoding/base64"

// base64RawURL нужен тестам курсора: они собирают заведомо негодные курсоры
// в том же кодировании, что и настоящие, — иначе проверялась бы только
// стойкость к неверному base64, а не к неверному содержимому.
func base64RawURL(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}
