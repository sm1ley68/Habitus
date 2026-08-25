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
// никакой ИИ не отказывал. Диагноз по такому тексту невозможен: он одинаков
// и для «модель ранжирования не уложилась в бюджет», и для «сервис не запущен».

func TestSearchTimeoutNamesTheBudget(t *testing.T) {
	code, message := mapMLError(client.ErrTimeout, 70*time.Second)

	if code != "search_timeout" {
		t.Fatalf("код = %q, ожидался search_timeout", code)
	}
	if !strings.Contains(message, "70") {
		t.Fatalf("в тексте должен быть бюджет в секундах: %q", message)
	}
	if strings.Contains(strings.ToLower(message), "ии") {
		t.Fatalf("таймаут ранжирования — не отказ ИИ: %q", message)
	}
}

func TestUnavailableSaysServiceIsUnreachable(t *testing.T) {
	code, message := mapMLError(fmt.Errorf("%w: dial tcp: connection refused", client.ErrUnavailable), time.Minute)

	if code != "ml_unavailable" {
		t.Fatalf("код = %q, ожидался ml_unavailable", code)
	}
	if message == "" {
		t.Fatal("причина должна доезжать до пользователя текстом")
	}
}

func TestServerErrorIsNotReportedAsDatabaseFailure(t *testing.T) {
	// До этой правки 5xx от ML отдавался кодом db_error — и разработчик шёл
	// чинить базу, которая ни при чём.
	code, _ := mapMLError(client.ErrServer, time.Minute)

	if code != "ml_error" {
		t.Fatalf("код = %q, ожидался ml_error", code)
	}
}

func TestBadResponseCarriesTheStatus(t *testing.T) {
	// Ровно этот случай стоил дня отладки: ML_SERVICE_URL смотрел на чужой
	// процесс, тот отвечал 404, а пользователь видел «внутреннюю ошибку».
	code, message := mapMLError(fmt.Errorf("%w: status 404", client.ErrBadResponse), time.Minute)

	if code != "ml_bad_response" {
		t.Fatalf("код = %q, ожидался ml_bad_response", code)
	}
	if !strings.Contains(message, "404") {
		t.Fatalf("статус ответа обязан быть в тексте: %q", message)
	}
}

func TestUnknownErrorStaysInternal(t *testing.T) {
	code, _ := mapMLError(errors.New("что-то новое"), time.Minute)

	if code != "internal_error" {
		t.Fatalf("код = %q, ожидался internal_error", code)
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
