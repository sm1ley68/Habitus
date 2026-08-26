package handlers

import (
	"encoding/json"
	"testing"
)

// Тест формы ответа: гостевой вход отдаёт то же тело, что и login/register,
// плюс явный признак — фронт по нему решает, показывать ли «Зарегистрируйтесь».
func TestGuestResponseShape(t *testing.T) {
	body := guestResponseBody("11111111-1111-1111-1111-111111111111", "Гость")

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"id", "email", "name", "is_guest"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("в ответе нет поля %q", key)
		}
	}
	if got["is_guest"] != true {
		t.Fatalf("is_guest = %v, ожидалось true", got["is_guest"])
	}
	if got["email"] != "" {
		t.Fatalf("email = %v, у гостя его нет", got["email"])
	}
}
