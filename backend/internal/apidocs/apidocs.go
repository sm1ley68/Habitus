// Package apidocs embeds the Partner API contract and its human documentation.
//
// Спецификация и страница документации вшиты в бинарь, а не читаются с диска
// намеренно: документ, который может разъехаться с задеплоенным кодом, хуже,
// чем его отсутствие — по нему интеграцию пишут, а потом она не работает.
package apidocs

import _ "embed"

//go:embed openapi.json
var openAPISpec []byte

//go:embed docs.html
var docsPage []byte

// OpenAPI — спецификация OpenAPI 3.1 в JSON.
func OpenAPI() []byte { return openAPISpec }

// DocsHTML — страница документации для разработчиков.
func DocsHTML() []byte { return docsPage }
