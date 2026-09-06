package http

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"habitus-backend/internal/apidocs"
	"habitus-backend/internal/http/middleware"
)

// Спецификация, которая расходится с кодом, хуже, чем её отсутствие: по ней
// пишут интеграцию, а потом она не работает. Этот тест держит их сцепленными —
// новая ручка без описания в openapi.json роняет сборку.

var pathParam = regexp.MustCompile(`:([A-Za-z_][A-Za-z0-9_]*)`)

// routePath переводит путь fiber (`/x/:id`) в путь OpenAPI (`/x/{id}`).
func routePath(p string) string {
	return pathParam.ReplaceAllString(p, "{$1}")
}

func registeredPartnerRoutes(t *testing.T) map[string]bool {
	t.Helper()
	app := fiber.New()
	// Обработчики нулевые: тест перечисляет маршруты, а не вызывает их.
	// Квоты нужны настоящие — из них при регистрации берутся middleware.
	RegisterPartnerRoutes(app, PartnerHandlers{}, PartnerDeps{
		Quotas: middleware.NewPartnerQuotas(10, 10), DocsURL: "https://example.test/docs",
	})

	out := map[string]bool{}
	for _, r := range app.GetRoutes() {
		switch r.Method {
		case fiber.MethodGet, fiber.MethodPost, fiber.MethodPatch,
			fiber.MethodPut, fiber.MethodDelete:
		default:
			continue // USE/HEAD/OPTIONS — не операции контракта
		}
		// Точка монтирования группы (`/partner/v1` без сегментов дальше) —
		// это цепочка middleware, а не операция контракта.
		if !strings.HasPrefix(r.Path, "/partner/v1/") {
			continue
		}
		out[strings.ToLower(r.Method)+" "+routePath(r.Path)] = true
	}
	return out
}

func specOperations(t *testing.T) map[string]bool {
	t.Helper()
	var spec struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apidocs.OpenAPI(), &spec); err != nil {
		t.Fatalf("openapi.json не разбирается: %v", err)
	}
	out := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "patch", "put", "delete":
				out[method+" "+path] = true
			}
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestOpenAPICoversEveryPartnerRoute(t *testing.T) {
	routes := registeredPartnerRoutes(t)
	spec := specOperations(t)

	var missing []string
	for _, route := range keys(routes) {
		if !spec[route] {
			missing = append(missing, route)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("ручки есть в коде, но не описаны в openapi.json:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

func TestOpenAPIDescribesNoPhantomRoutes(t *testing.T) {
	routes := registeredPartnerRoutes(t)
	spec := specOperations(t)

	var phantom []string
	for _, op := range keys(spec) {
		if !routes[op] {
			phantom = append(phantom, op)
		}
	}
	if len(phantom) > 0 {
		t.Fatalf("openapi.json описывает ручки, которых нет в коде:\n  %s",
			strings.Join(phantom, "\n  "))
	}
}

// TestOpenAPISpecIsWellFormed ловит битые ссылки $ref: генератор клиента
// падает на них молча, а разработчик партнёра — уже на своей стороне.
func TestOpenAPISpecIsWellFormed(t *testing.T) {
	var spec map[string]any
	if err := json.Unmarshal(apidocs.OpenAPI(), &spec); err != nil {
		t.Fatalf("openapi.json не разбирается: %v", err)
	}
	if spec["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v; ожидалась версия 3.1.0", spec["openapi"])
	}
	for _, ref := range collectRefs(spec) {
		if !resolves(spec, ref) {
			t.Errorf("битая ссылка в спецификации: %s", ref)
		}
	}
}

func collectRefs(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "$ref" {
				if s, ok := child.(string); ok {
					out = append(out, s)
				}
				continue
			}
			out = append(out, collectRefs(child)...)
		}
	case []any:
		for _, child := range v {
			out = append(out, collectRefs(child)...)
		}
	}
	return out
}

func resolves(spec map[string]any, ref string) bool {
	if !strings.HasPrefix(ref, "#/") {
		return false
	}
	var node any = spec
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return false
		}
		node, ok = m[part]
		if !ok {
			return false
		}
	}
	return true
}
