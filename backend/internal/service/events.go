// events.go — журнал продуктовых событий: воронка от поиска до заявки.
// Технические метрики (latency, degraded, 429) на вопрос «дошёл ли человек до
// конца» не отвечают, а `uv run habitus eval` меряет качество оффлайн.
//
// Запись неблокирующая и через собственный контекст: контекст HTTP-запроса
// умирает сразу после ответа, и запись «в хвосте» на нём терялась бы гонкой.
package service

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"habitus-backend/internal/domain"
)

// Имена событий. Строки, а не enum: журнал читается SQL-запросами, и константы
// нужны только чтобы не разъехались написания в разных хендлерах.
const (
	EventGuestCreated   = "guest_created"
	EventGuestUpgraded  = "guest_upgraded"
	EventSearchStarted  = "search_started"
	EventPassportOpened = "passport_opened"
	EventFavoriteAdded  = "favorite_added"
	EventFeedbackGiven  = "feedback_given"
	EventLeadSent       = "lead_sent"
)

// eventStore — часть EventRepo.
type eventStore interface {
	Insert(ctx context.Context, e domain.ProductEvent) error
}

// eventWriteTimeout — предел на одну запись. Без него зависший INSERT
// остановил бы воркер навсегда, и журнал молча перестал бы наполняться.
const eventWriteTimeout = 5 * time.Second

type EventRecorder struct {
	store  eventStore
	events chan domain.ProductEvent
}

// NewEventRecorder. buffer — сколько событий переживут всплеск нагрузки;
// переполнение теряет событие, и это осознанный размен: телеметрия не имеет
// права замедлить ответ пользователю.
func NewEventRecorder(store eventStore, buffer int) *EventRecorder {
	if buffer <= 0 {
		buffer = 1024
	}
	return &EventRecorder{store: store, events: make(chan domain.ProductEvent, buffer)}
}

// Start поднимает единственного писателя. Один, а не пул: журнал — это
// последовательные вставки, и конкуренция за соединения пула тут ни к чему.
func (r *EventRecorder) Start(ctx context.Context) {
	if r == nil {
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-r.events:
				r.write(e)
			}
		}
	}()
}

func (r *EventRecorder) write(e domain.ProductEvent) {
	// Собственный контекст: запрос, породивший событие, уже завершён.
	ctx, cancel := context.WithTimeout(context.Background(), eventWriteTimeout)
	defer cancel()
	if err := r.store.Insert(ctx, e); err != nil {
		// Логируем и живём дальше: моргнувшая БД не должна выключать
		// телеметрию до конца жизни процесса.
		log.Error().Err(err).Str("kind", e.Kind).Msg("product event write failed")
	}
}

// Record никогда не блокирует и никогда не паникует — в том числе на nil
// рекордере (телеметрия выключена: так собраны тесты сервисов).
func (r *EventRecorder) Record(e domain.ProductEvent) {
	if r == nil {
		return
	}
	select {
	case r.events <- e:
	default:
		log.Warn().Str("kind", e.Kind).Msg("product event dropped: buffer full")
	}
}
