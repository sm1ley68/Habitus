package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"habitus-backend/internal/client"
)

// Раньше любой отказ ML схлопывался в «Не удалось получить ответ от ИИ» с кодом
// llm_timeout. Это враньё в трёх из четырёх случаев: ML мог не подняться, мог
// вернуть 5xx, мог ответить 404 на неизвестный путь — и ни в одном из них
// никакой ИИ не отказывал. Следующий шаг — не только назвать класс отказа, но
// и донести ПРИЧИНУ: какая стадия упала, что именно сказал ML, что чинить.

func TestSearchTimeoutNamesTheBudget(t *testing.T) {
	fail := mapMLError(client.ErrTimeout, 70*time.Second)

	if fail.Code != "search_timeout" {
		t.Fatalf("код = %q, ожидался search_timeout", fail.Code)
	}
	if !strings.Contains(fail.Message, "70") {
		t.Fatalf("в тексте должен быть бюджет в секундах: %q", fail.Message)
	}
	if strings.Contains(strings.ToLower(fail.Message), "ии") {
		t.Fatalf("таймаут ранжирования — не отказ ИИ: %q", fail.Message)
	}
	if fail.Hint == "" {
		t.Fatal("на таймауте пользователю нужно сказать, что делать")
	}
}

func TestUnavailableSaysServiceIsUnreachable(t *testing.T) {
	fail := mapMLError(fmt.Errorf("%w: dial tcp: connection refused", client.ErrUnavailable), time.Minute)

	if fail.Code != "ml_unavailable" {
		t.Fatalf("код = %q, ожидался ml_unavailable", fail.Code)
	}
	if fail.Message == "" {
		t.Fatal("причина должна доезжать до пользователя текстом")
	}
}

func TestServerErrorIsNotReportedAsDatabaseFailure(t *testing.T) {
	// До этой правки 5xx от ML отдавался кодом db_error — и разработчик шёл
	// чинить базу, которая ни при чём.
	fail := mapMLError(client.ErrServer, time.Minute)

	if fail.Code != "ml_error" {
		t.Fatalf("код = %q, ожидался ml_error", fail.Code)
	}
}

func TestBadResponseCarriesTheStatus(t *testing.T) {
	// Ровно этот случай стоил дня отладки: ML_SERVICE_URL смотрел на чужой
	// процесс, тот отвечал 404, а пользователь видел «внутреннюю ошибку».
	fail := mapMLError(&client.MLFailure{
		Kind: client.ErrBadResponse, Endpoint: "/search", Status: 404,
		Detail: "<html><body>404 Not Found</body></html>",
	}, time.Minute)

	if fail.Code != "ml_bad_response" {
		t.Fatalf("код = %q, ожидался ml_bad_response", fail.Code)
	}
	if !strings.Contains(fail.Cause, "404") {
		t.Fatalf("статус ответа обязан быть в причине: %q", fail.Cause)
	}
	if !strings.Contains(fail.Cause, "/search") {
		t.Fatalf("без ручки непонятно, куда ушёл запрос: %q", fail.Cause)
	}
}

func TestUnknownErrorStaysInternal(t *testing.T) {
	fail := mapMLError(errors.New("что-то новое"), time.Minute)

	if fail.Code != "internal_error" {
		t.Fatalf("код = %q, ожидался internal_error", fail.Code)
	}
	if !strings.Contains(fail.Cause, "что-то новое") {
		t.Fatalf("текст незнакомой ошибки — единственная улика: %q", fail.Cause)
	}
}

// --- диагноз, пришедший из ML ---

func TestStructuredDiagnosisWins(t *testing.T) {
	// ML назвала причину сама (habitus/online/errors.py). Пересказывать её
	// общим «сервис поиска вернул ошибку» — значит выбросить диагноз, который
	// уже посчитан.
	fail := mapMLError(&client.MLFailure{
		Kind: client.ErrServer, Endpoint: "/search", Status: 503,
		Code: "db_unavailable", Stage: "retrieval",
		Detail:  "Нет связи с базой: connection refused",
		Hint:    "Postgres по адресу db:5432/habitus не отвечает",
		Timings: map[string]float64{"parse": 812.5},
		Elapsed: 3 * time.Second,
	}, time.Minute)

	if fail.Code != "db_unavailable" {
		t.Fatalf("код = %q — код ML конкретнее классового ml_error", fail.Code)
	}
	if !strings.Contains(fail.Message, "connection refused") {
		t.Fatalf("причина от ML = %q", fail.Message)
	}
	if !strings.Contains(fail.Hint, "db:5432") {
		t.Fatalf("подсказка от ML = %q", fail.Hint)
	}
	if !strings.Contains(fail.Cause, "retrieval") {
		t.Fatalf("стадия падения обязана быть в причине: %q", fail.Cause)
	}
}

func TestCauseNamesTheSlowestStageThatManagedToRun(t *testing.T) {
	fail := mapMLError(&client.MLFailure{
		Kind: client.ErrServer, Endpoint: "/search", Status: 500,
		Code: "internal_error", Detail: "RuntimeError: boom", Stage: "rerank",
		Timings: map[string]float64{"parse": 900, "retrieval": 47300},
	}, time.Minute)

	if !strings.Contains(fail.Cause, "retrieval") {
		t.Fatalf("самая долгая успевшая стадия — половина диагноза: %q", fail.Cause)
	}
	if !strings.Contains(fail.Cause, "47") {
		t.Fatalf("её длительность обязана быть в причине: %q", fail.Cause)
	}
}

func TestTimeoutCauseSaysHowLongItActuallyWaited(t *testing.T) {
	fail := mapMLError(&client.MLFailure{
		Kind: client.ErrTimeout, Endpoint: "/search",
		Elapsed: 61200 * time.Millisecond,
		Detail:  "context deadline exceeded",
	}, 60*time.Second)

	if fail.Code != "search_timeout" {
		t.Fatalf("код = %q", fail.Code)
	}
	if !strings.Contains(fail.Cause, "61") {
		t.Fatalf("сколько реально прождали = %q", fail.Cause)
	}
	if !strings.Contains(fail.Hint, "ML_SEARCH_TIMEOUT_S") &&
		!strings.Contains(fail.Hint, "RERANK_POOL_N") {
		t.Fatalf("подсказка обязана называть ручки, которыми это чинят: %q", fail.Hint)
	}
}

func TestUnavailableCauseNamesTheAddressItCouldNotReach(t *testing.T) {
	fail := mapMLError(&client.MLFailure{
		Kind: client.ErrUnavailable, Endpoint: "/search",
		Detail: `Post "http://ml:8000/search": dial tcp 172.18.0.3:8000: connect: connection refused`,
	}, time.Minute)

	if fail.Code != "ml_unavailable" {
		t.Fatalf("код = %q", fail.Code)
	}
	if !strings.Contains(fail.Cause, "connection refused") {
		t.Fatalf("текст сетевой ошибки несёт хост и порт: %q", fail.Cause)
	}
	if !strings.Contains(fail.Hint, "ML_SERVICE_URL") {
		t.Fatalf("подсказка = %q", fail.Hint)
	}
}

func TestNoInventedCauseWhenNothingIsKnown(t *testing.T) {
	// Голый сентинел без MLFailure: технической улики нет. Придумывать
	// правдоподобную причину нельзя — поле просто остаётся пустым.
	fail := mapMLError(client.ErrServer, time.Minute)

	if fail.Cause != "" {
		t.Fatalf("причина выдумана из ничего: %q", fail.Cause)
	}
}

func TestSlowestStageNamesTheCulprit(t *testing.T) {
	stage, ms := slowestStage(map[string]float64{
		"parse": 8200, "embed": 6400, "retrieval": 700, "rerank": 73900,
	})

	if stage != "rerank" {
		t.Fatalf("виновник = %q, ожидался rerank", stage)
	}
	if ms != 73900 {
		t.Fatalf("длительность = %v", ms)
	}
}

func TestSlowestStageOnEmptyTimings(t *testing.T) {
	// ML прислала ответ без разбивки — сказать про стадии нечего, и выдумывать
	// «самую долгую» из пустоты нельзя.
	stage, ms := slowestStage(nil)

	if stage != "" || ms != 0 {
		t.Fatalf("на пустых timings ожидалось (\"\", 0), получено (%q, %v)", stage, ms)
	}
}
