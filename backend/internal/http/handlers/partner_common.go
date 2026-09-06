// partner_common.go — общая форма ответов Partner API: списки, курсоры,
// разбор параметров. Всё, что должно выглядеть одинаково во всех ручках,
// собрано здесь, чтобы не расходилось от ручки к ручке.
package handlers

import (
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
)

// Каждый ресурс несёт поле object с именем своего типа. Это позволяет
// клиенту разбирать ответ, не завися от того, из какой ручки он приехал, —
// то же соглашение, что у крупных платёжных API.
const (
	ObjectList            = "list"
	ObjectPartner         = "partner"
	ObjectSearch          = "search"
	ObjectSearchResult    = "search_result"
	ObjectProperty        = "property"
	ObjectAnswer          = "answer"
	ObjectListing         = "listing"
	ObjectLead            = "lead"
	ObjectWebhook         = "webhook"
	ObjectWebhookDelivery = "webhook_delivery"
	ObjectGeoLayers       = "geo_layers"
)

const (
	partnerDefaultLimit = 25
	partnerMaxLimit     = 100
)

// Page — разобранные параметры страницы. Курсор непрозрачный: внутри лежит
// смещение, но клиент об этом знать не должен — иначе он начнёт его считать
// сам, и сменить схему пагинации станет ломающим изменением.
type Page struct {
	Limit  int
	Offset int
}

// ParsePage читает limit и cursor. Кривой курсор — ошибка, а не тихий сброс
// на первую страницу: молчаливый сброс закольцовывает клиента на первой
// странице навсегда, и заметить это он может только по счётчику.
func ParsePage(c *fiber.Ctx) (Page, error) {
	limit := partnerDefaultLimit
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return Page{}, apperr.Validation("limit должен быть целым числом больше нуля").
				WithParam("limit")
		}
		limit = n
	}
	if limit > partnerMaxLimit {
		limit = partnerMaxLimit
	}

	offset := 0
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		n, err := decodeCursor(raw)
		if err != nil {
			return Page{}, apperr.Validation("Курсор не распознан — передайте next_cursor из предыдущего ответа").
				WithParam("cursor")
		}
		offset = n
	}
	return Page{Limit: limit, Offset: offset}, nil
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(offset)))
}

func decodeCursor(raw string) (int, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, err
	}
	value := string(decoded)
	if !strings.HasPrefix(value, "o:") {
		return 0, apperr.Validation("bad cursor")
	}
	n, err := strconv.Atoi(value[2:])
	if err != nil || n < 0 {
		return 0, apperr.Validation("bad cursor")
	}
	return n, nil
}

// ListResponse — единая обёртка списка. has_more и next_cursor считаются от
// total и текущей страницы: клиент листает до has_more == false и никогда не
// собирает курсор руками.
//
// consumed — сколько строк выборки ушло на эту страницу. Обычно это len(data),
// но не всегда: страница результатов поиска отсеивает объекты, пропавшие из
// витрины уже после подбора, и курсор обязан сдвинуться на ПРОЧИТАННОЕ, а не
// на показанное — иначе следующая страница повторит уже отданные объекты, и
// клиент будет ходить по кругу.
func ListResponse(itemType string, data []fiber.Map, page Page, total, consumed int) fiber.Map {
	next := page.Offset + consumed
	hasMore := next < total
	out := fiber.Map{
		"object":      ObjectList,
		"item_object": itemType,
		"data":        data,
		"total":       total,
		"has_more":    hasMore,
		"next_cursor": nil,
	}
	if hasMore {
		out["next_cursor"] = encodeCursor(next)
	}
	return out
}

// partnerUUID разбирает идентификатор из пути. Кривой uuid неотличим от
// несуществующего ресурса, поэтому 404, а не 400 — тот же выбор, что уже
// сделан для чатов и объявлений кабинета.
func partnerUUID(c *fiber.Ctx, name string, notFound func() *apperr.Error) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil {
		return uuid.Nil, notFound()
	}
	return id, nil
}
