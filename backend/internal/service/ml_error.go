// ml_error.go — перевод отказа ML в то, что видит пользователь.
//
// Прошлый шаг развёл четыре класса отказов по кодам, но текст оставался одной
// фразой на класс: «Сервис поиска вернул ошибку. Загляните в его логи» одинаково
// звучало и для непонятой базы, и для кончившейся квоты LLM, и для отсутствующей
// таблицы. Теперь наружу едут три разных поля:
//
//	message — что произошло, человеческим языком;
//	cause   — техническая улика: ручка, статус, стадия, тайминги, текст ошибки;
//	hint    — что с этим делать: какую ручку крутить, что проверить.
//
// Разделение нужно, потому что читателей два. Пользователю хватает message, а
// разработчику без cause/hint приходится лезть в логи двух контейнеров, чтобы
// узнать то, что на момент отказа уже было известно.
package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"habitus-backend/internal/client"
)

// userFacingError — отказ, каким он уезжает в SSE-событие error и в конверт REST.
type userFacingError struct {
	Code    string
	Message string
	Cause   string
	Hint    string
}

// mapMLError переводит отказ ML в код, текст, причину и подсказку.
//
// budget нужен ветке таймаута: без числа «попробуйте ещё раз» — совет наугад,
// а с числом видно, что упёрлись именно в бюджет, а не в сбой.
func mapMLError(err error, budget time.Duration) userFacingError {
	var failure *client.MLFailure
	if errors.As(err, &failure) {
		return describeFailure(failure, budget)
	}
	// Голый сентинел: технической улики нет, cause остаётся пустым — выдумывать
	// правдоподобную причину хуже, чем не сказать ничего.
	return byKind(err, budget, "")
}

// describeFailure раскрывает *client.MLFailure: сперва диагноз, который ML
// посчитала сама, потом — классовая ветка с собранной уликой.
func describeFailure(f *client.MLFailure, budget time.Duration) userFacingError {
	cause := mlCause(f, budget)

	// ML назвала код и причину сама (habitus/online/errors.py) — это точнее
	// любого пересказа на нашей стороне, и подменять его классовым ml_error
	// значит выбросить уже посчитанный диагноз.
	if f.Code != "" {
		return userFacingError{
			Code:    f.Code,
			Message: firstNonEmpty(f.Detail, "Сервис поиска вернул ошибку"),
			Cause:   cause,
			Hint:    f.Hint,
		}
	}
	return byKind(f.Kind, budget, cause)
}

// byKind — классовая ветка: что сказать, когда конкретного диагноза от ML нет.
func byKind(err error, budget time.Duration, cause string) userFacingError {
	switch {
	case errors.Is(err, client.ErrTimeout):
		return userFacingError{
			Code: "search_timeout",
			Message: fmt.Sprintf("Поиск не уложился в %d с. Похоже, сервису не "+
				"хватает мощности — попробуйте ещё раз или упростите запрос",
				int(budget.Seconds())),
			Cause: cause,
			Hint: "Если повторяется — поднимите ML_SEARCH_TIMEOUT_S или уменьшите " +
				"RERANK_POOL_N: реранк линеен по числу пар и занимает львиную " +
				"долю времени поиска",
		}
	case errors.Is(err, client.ErrUnavailable):
		return userFacingError{
			Code:    "ml_unavailable",
			Message: "Сервис поиска не отвечает — похоже, он не запущен",
			Cause:   cause,
			Hint:    "Проверьте, поднят ли ML-контейнер и куда смотрит ML_SERVICE_URL",
		}
	case errors.Is(err, client.ErrServer):
		return userFacingError{
			Code:    "ml_error",
			Message: "Сервис поиска вернул ошибку",
			Cause:   cause,
			Hint:    "Трассировка — в логах ML-сервиса",
		}
	case errors.Is(err, client.ErrBadResponse):
		return userFacingError{
			Code:    "ml_bad_response",
			Message: "Сервис поиска ответил неожиданно",
			Cause:   cause,
			Hint: "Так отвечает чужой процесс на месте ML: сверьте ML_SERVICE_URL " +
				"с портом, который слушает ML-сервис",
		}
	default:
		return userFacingError{
			Code:    "internal_error",
			Message: "Внутренняя ошибка сервера",
			Cause:   firstNonEmpty(cause, errText(err)),
			Hint:    "",
		}
	}
}

// mlCause собирает техническую улику из того, что РЕАЛЬНО известно об отказе.
// Каждая часть добавляется только когда она есть: пустая стадия или нулевой
// статус — это «нечего сказать», а не повод дописать в причину ноль.
func mlCause(f *client.MLFailure, budget time.Duration) string {
	where := "ML " + f.Endpoint
	if f.Status != 0 {
		where += fmt.Sprintf(" → HTTP %d", f.Status)
	}
	parts := []string{where}

	if f.Stage != "" {
		parts = append(parts, "упало на стадии "+f.Stage)
	}
	if slowest, ms := slowestStage(f.Timings); slowest != "" {
		parts = append(parts, fmt.Sprintf("дольше всего отработала стадия %s (%.1f с)",
			slowest, ms/1000))
	}
	if errors.Is(f.Kind, client.ErrTimeout) && f.Elapsed > 0 {
		waited := fmt.Sprintf("ждали %.0f с", f.Elapsed.Seconds())
		// Бюджет дописываем, только если он известен: на REST-путях (mlDiagnosis)
		// его нет, и «при бюджете 0 с» было бы выдуманным фактом.
		if budget > 0 {
			waited += fmt.Sprintf(" при бюджете %.0f с", budget.Seconds())
		}
		parts = append(parts, waited)
	}
	// Текст ответа дублировать незачем, если он уже уехал в message как
	// диагноз ML; во всех остальных случаях это единственная улика.
	if f.Code == "" && f.Detail != "" {
		parts = append(parts, f.Detail)
	}
	return strings.Join(parts, "; ")
}

// mlDiagnosis — причина и подсказка для REST-путей, где текст для пользователя
// уже сформулирован продуктово («Витрина не приняла объявление») и меняться не
// должен, а вот почему именно не приняла — до сих пор было неизвестно никому.
func mlDiagnosis(err error) (cause, hint string) {
	fail := mapMLError(err, 0)
	return fail.Cause, fail.Hint
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
