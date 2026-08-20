// metrics.go — счётчики backend'а в формате Prometheus text exposition,
// написанные вручную: задача прямо запрещает тащить клиентскую библиотеку
// Prometheus (Task 8, «эксплуатация»). Один процесс — один набор счётчиков в
// памяти: как и in-memory лок стрима в search_stream_service.go, это корректно
// для одной реплики backend'а, а не для горизонтального масштабирования.
package observability

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Metrics — потокобезопасное хранилище счётчиков одной реплики. Пишут
// конкурентно и middleware (HTTP-запросы, рейт-лимит), и сервисы (вызовы ML),
// поэтому всё под одним мьютексом — объёмы небольшие, лишняя гранулярность
// локов тут не нужна.
type Metrics struct {
	mu sync.Mutex

	httpRequestsTotal map[[2]string]uint64 // (route, status) -> count

	mlCallSum   map[string]float64 // kind -> сумма секунд
	mlCallCount map[string]uint64  // kind -> количество вызовов

	mlDegradedTotal map[string]uint64 // layer -> количество

	mlStageSum   map[string]float64 // stage -> сумма секунд (из timings ответа ML)
	mlStageCount map[string]uint64  // stage -> количество замеров

	rateLimitedTotal uint64
}

func NewMetrics() *Metrics {
	return &Metrics{
		httpRequestsTotal: make(map[[2]string]uint64),
		mlCallSum:         make(map[string]float64),
		mlCallCount:       make(map[string]uint64),
		mlDegradedTotal:   make(map[string]uint64),
		mlStageSum:        make(map[string]float64),
		mlStageCount:      make(map[string]uint64),
	}
}

// Default — глобальный экземпляр процесса, как глобальный log.Logger у
// zerolog в logger.go. Middleware и сервисы пишут прямо в него; /metrics его
// же читает — отдельного слоя регистрации/DI заводить незачем (см. бриф).
var Default = NewMetrics()

// IncHTTPRequest — habitus_http_requests_total{route,status}. route — паттерн
// маршрута (fiber Route().Path), а не сырой URL: иначе id чата/объекта в пути
// разрастили бы кардинальность до одной метки на запрос.
func (m *Metrics) IncHTTPRequest(route, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpRequestsTotal[[2]string{route, status}]++
}

// ObserveMLCall — habitus_ml_call_seconds{kind}: длительность одного похода в
// ML-сервис (search/explain/dossier/object_ask), в секундах.
func (m *Metrics) ObserveMLCall(kind string, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mlCallSum[kind] += seconds
	m.mlCallCount[kind]++
}

// IncMLDegraded — habitus_ml_degraded_total{layer}: слой, который ML реально
// вернула в поле degraded ответа /search.
func (m *Metrics) IncMLDegraded(layer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mlDegradedTotal[layer]++
}

// ObserveMLStage — habitus_ml_stage_seconds{stage}: одна стадия из поля
// timings ответа ML (Task 1), в секундах. Стадию, которой нет в ответе, сюда
// передавать нельзя — вызывающая сторона обязана перебирать только то, что
// реально пришло в timings, без выдуманных нулей.
func (m *Metrics) ObserveMLStage(stage string, seconds float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mlStageSum[stage] += seconds
	m.mlStageCount[stage]++
}

// IncRateLimited — habitus_rate_limited_total: сколько раз сработал рейт-лимит
// LLM-ручек (без меток — это разбирают эксплуатационно через логи 429).
func (m *Metrics) IncRateLimited() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rateLimitedTotal++
}

// Render формирует тело ответа /metrics в формате Prometheus text exposition
// (версия 0.0.4). Ключи каждой метрики сортируются — вывод детерминирован,
// что важно и для тестов, и для стабильных диффов между последовательными
// snapshot'ами при отладке.
func (m *Metrics) Render() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var b strings.Builder

	b.WriteString("# HELP habitus_http_requests_total Общее число HTTP-запросов к шлюзу.\n")
	b.WriteString("# TYPE habitus_http_requests_total counter\n")
	for _, k := range sortedPairKeys(m.httpRequestsTotal) {
		fmt.Fprintf(&b, "habitus_http_requests_total{route=%s,status=%s} %d\n",
			quoteLabel(k[0]), quoteLabel(k[1]), m.httpRequestsTotal[k])
	}

	b.WriteString("# HELP habitus_ml_call_seconds Длительность вызовов ML-сервиса, сек.\n")
	b.WriteString("# TYPE habitus_ml_call_seconds summary\n")
	for _, kind := range sortedKeys(m.mlCallCount) {
		fmt.Fprintf(&b, "habitus_ml_call_seconds_sum{kind=%s} %s\n", quoteLabel(kind), formatFloat(m.mlCallSum[kind]))
		fmt.Fprintf(&b, "habitus_ml_call_seconds_count{kind=%s} %d\n", quoteLabel(kind), m.mlCallCount[kind])
	}

	b.WriteString("# HELP habitus_ml_degraded_total Сколько раз ML вернула слой в degraded.\n")
	b.WriteString("# TYPE habitus_ml_degraded_total counter\n")
	for _, layer := range sortedKeys(m.mlDegradedTotal) {
		fmt.Fprintf(&b, "habitus_ml_degraded_total{layer=%s} %d\n", quoteLabel(layer), m.mlDegradedTotal[layer])
	}

	b.WriteString("# HELP habitus_ml_stage_seconds Тайминги стадий пайплайна ML (поле timings ответа /search), сек.\n")
	b.WriteString("# TYPE habitus_ml_stage_seconds summary\n")
	for _, stage := range sortedKeys(m.mlStageCount) {
		fmt.Fprintf(&b, "habitus_ml_stage_seconds_sum{stage=%s} %s\n", quoteLabel(stage), formatFloat(m.mlStageSum[stage]))
		fmt.Fprintf(&b, "habitus_ml_stage_seconds_count{stage=%s} %d\n", quoteLabel(stage), m.mlStageCount[stage])
	}

	b.WriteString("# HELP habitus_rate_limited_total Сколько раз сработал рейт-лимит LLM-ручек.\n")
	b.WriteString("# TYPE habitus_rate_limited_total counter\n")
	fmt.Fprintf(&b, "habitus_rate_limited_total %d\n", m.rateLimitedTotal)

	return b.String()
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// quoteLabel — минимальное экранирование значения метки Prometheus (кавычки и
// обратный слэш). Значения меток в этом проекте — паттерны роутов, kind/layer/
// stage/status, экзотических символов не бывает, но экранирование дешёвое и
// делает формат корректным при любом вводе.
func quoteLabel(v string) string {
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedPairKeys(m map[[2]string]uint64) [][2]string {
	keys := make([][2]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	return keys
}
