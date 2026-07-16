package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"habitus-backend/internal/client"
	"habitus-backend/internal/http/sse"
	"habitus-backend/internal/repository"
)

type objectStreamKey struct {
	ChatID   uuid.UUID
	ObjectID string
}

type ObjectAskService struct {
	searches *repository.ChatSearchRepo
	ml       *client.MLClient
	timeout  time.Duration

	mu       sync.Mutex
	inFlight map[objectStreamKey]struct{}
}

func NewObjectAskService(searches *repository.ChatSearchRepo, ml *client.MLClient,
	timeout time.Duration) *ObjectAskService {
	return &ObjectAskService{searches: searches, ml: ml, timeout: timeout,
		inFlight: make(map[objectStreamKey]struct{})}
}

func (s *ObjectAskService) TotalBudget() time.Duration { return s.timeout + 15*time.Second }

func (s *ObjectAskService) TryLock(chatID uuid.UUID, objectID string) bool {
	key := objectStreamKey{ChatID: chatID, ObjectID: objectID}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inFlight[key]; exists {
		return false
	}
	s.inFlight[key] = struct{}{}
	return true
}

func (s *ObjectAskService) Unlock(chatID uuid.UUID, objectID string) {
	s.mu.Lock()
	delete(s.inFlight, objectStreamKey{ChatID: chatID, ObjectID: objectID})
	s.mu.Unlock()
}

type objectAskOutcome struct {
	response *client.ObjectAskResponse
	err      error
}

func (s *ObjectAskService) Run(ctx context.Context, chatID uuid.UUID, objectID,
	question string, passport ObjectPassport, writer *sse.Writer) {
	result, err := s.searches.GetResult(ctx, chatID, objectID)
	if err != nil {
		_ = writer.WriteEvent("error", ErrorEvent{Code: "db_error", Message: "Не удалось загрузить контекст объекта"})
		return
	}
	search, err := s.searches.GetSearch(ctx, result.SearchID)
	if err != nil {
		_ = writer.WriteEvent("error", ErrorEvent{Code: "db_error", Message: "Не удалось загрузить контекст поиска"})
		return
	}
	passportJSON, err := json.Marshal(passport)
	if err != nil {
		_ = writer.WriteEvent("error", ErrorEvent{Code: "internal_error", Message: "Не удалось подготовить досье"})
		return
	}
	var passportMap map[string]any
	_ = json.Unmarshal(passportJSON, &passportMap)
	searchContext := map[string]any{
		"chat_id": chatID.String(), "object_id": objectID,
		"raw_query": search.RawQuery, "parsed_query": search.ParsedQuery,
		"relaxed": nonNilStrings(search.Relaxed), "degraded": nonNilStrings(search.Degraded),
	}

	mlCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	outcomeCh := make(chan objectAskOutcome, 1)
	go func() {
		response, askErr := s.ml.AskObject(mlCtx, client.ObjectAskRequest{
			Question: question, Passport: passportMap, SearchContext: searchContext,
		})
		outcomeCh <- objectAskOutcome{response: response, err: askErr}
	}()

	if err := writer.WriteEvent("agent_status", AgentStatusEvent{
		Agent: "orchestrator", Status: "processing", Message: "Проверяю факты досье…",
	}); err != nil {
		cancel()
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var outcome objectAskOutcome
	waiting := true
	for waiting {
		select {
		case outcome = <-outcomeCh:
			waiting = false
		case <-ticker.C:
			if err := writer.WriteEvent("agent_status", AgentStatusEvent{
				Agent: "orchestrator", Status: "processing", Message: "Сверяю ответ с источниками…",
			}); err != nil {
				cancel()
				return
			}
		case <-ctx.Done():
			cancel()
			return
		}
	}
	if outcome.err != nil {
		code, message := mapMLError(outcome.err)
		_ = writer.WriteEvent("error", ErrorEvent{Code: code, Message: message})
		return
	}
	parts := make([]string, 0, len(outcome.response.Sentences))
	for _, sentence := range outcome.response.Sentences {
		if text := strings.TrimSpace(sentence.Text); text != "" {
			parts = append(parts, text)
		}
	}
	answer := strings.Join(parts, " ")
	if answer == "" {
		answer = "Не знаю по этому объекту: в досье нет подтверждённых данных для ответа."
	}
	for _, token := range splitTokens(answer) {
		if err := writer.WriteEvent("text_token", TextTokenEvent{Token: token}); err != nil {
			cancel()
			return
		}
		time.Sleep(28 * time.Millisecond)
	}
	_ = writer.WriteEvent("stream_end", struct{}{})
}
