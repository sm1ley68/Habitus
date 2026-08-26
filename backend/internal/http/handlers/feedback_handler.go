package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"habitus-backend/internal/apperr"
	"habitus-backend/internal/http/middleware"
	"habitus-backend/internal/service"
)

type FeedbackHandler struct {
	feedback *service.FeedbackService
}

func NewFeedbackHandler(feedback *service.FeedbackService) *FeedbackHandler {
	return &FeedbackHandler{feedback: feedback}
}

type feedbackRequest struct {
	Verdict string `json:"verdict"`
	Reason  string `json:"reason"`
}

// Save implements POST /chats/{chat_id}/results/{object_id}/feedback.
func (h *FeedbackHandler) Save(c *fiber.Ctx) error {
	chatID, err := uuid.Parse(c.Params("chat_id"))
	if err != nil {
		return apperr.ChatNotFound()
	}
	objectID := c.Params("object_id")
	if objectID == "" {
		return apperr.ObjectNotFound()
	}
	var req feedbackRequest
	if err := c.BodyParser(&req); err != nil {
		return apperr.Validation("invalid request body")
	}

	if err := h.feedback.Save(c.Context(), middleware.UserID(c), chatID,
		objectID, req.Verdict, req.Reason); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
