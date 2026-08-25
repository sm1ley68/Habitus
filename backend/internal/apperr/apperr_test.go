package apperr

import "testing"

// Конверт ошибки нёс только code+message. Для «Витрина не приняла объявление»
// это означало, что продавец и разработчик видят одну и ту же фразу и ни один
// из них не может понять, ML ли не поднялся, база ли отвалилась или объявление
// не прошло валидацию на той стороне.

func TestCauseAndHintAreEmptyByDefault(t *testing.T) {
	e := Internal("не сработало")

	if e.Cause != "" || e.Hint != "" {
		t.Fatalf("пустой отказ не должен нести выдуманных полей: %+v", e)
	}
}

func TestWithCauseAndHintAttachTheDiagnosis(t *testing.T) {
	e := Internal("Витрина не приняла объявление").
		WithCause("ML /listings/owner-upsert → HTTP 503; упало на стадии embed").
		WithHint("Проверьте, поднят ли ML-контейнер")

	if e.Cause == "" || e.Hint == "" {
		t.Fatalf("диагноз не прикрепился: %+v", e)
	}
	if e.Code != "internal_error" || e.Status != 500 {
		t.Fatalf("класс отказа поменялся: %+v", e)
	}
	if e.Error() != "Витрина не приняла объявление" {
		t.Fatalf("Error() = %q — текст для пользователя меняться не должен", e.Error())
	}
}

func TestWithCauseDoesNotMutateTheOriginal(t *testing.T) {
	// Конструкторы возвращают новое значение на каждый вызов, но полагаться на
	// это нельзя: With* обязан быть чистым, иначе один обогащённый отказ
	// протечёт в соседний запрос.
	base := Internal("не сработало")
	enriched := base.WithCause("HTTP 503")

	if base.Cause != "" {
		t.Fatalf("исходный отказ мутировали: %+v", base)
	}
	if enriched.Cause != "HTTP 503" {
		t.Fatalf("копия не получила причину: %+v", enriched)
	}
}
