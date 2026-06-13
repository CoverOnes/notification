package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/CoverOnes/notification/internal/comms"
	"github.com/CoverOnes/notification/internal/platform/httpx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxCommsSendBody caps the POST /v1/comms/send request body (defense in depth on
// top of gin binding) — backend-security-design (io.LimitReader on every handler).
const maxCommsSendBody = 64 * 1024

// CommsHandler serves the S2S send API + the (stubbed) receipts webhook.
type CommsHandler struct {
	svc comms.CommsService
}

// NewCommsHandler returns a CommsHandler over the given service.
func NewCommsHandler(svc comms.CommsService) *CommsHandler {
	return &CommsHandler{svc: svc}
}

// sendRequestBody is the POST /v1/comms/send request payload.
type sendRequestBody struct {
	Channel        string            `json:"channel"        binding:"required"`
	To             string            `json:"to"             binding:"required"`
	TemplateID     string            `json:"templateId"     binding:"required"`
	Locale         string            `json:"locale"`
	Vars           map[string]string `json:"vars"`
	IdempotencyKey string            `json:"idempotencyKey" binding:"required"`
	UserID         *string           `json:"userId"`
}

// Send handles POST /v1/comms/send. It is S2S only (RequireServiceIdentity is
// applied at the router). Response is a 202 envelope {data:{sendId,status,deduped}}.
func (h *CommsHandler) Send(c *gin.Context) {
	// Bound the body before decoding (DoS defense).
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCommsSendBody)

	var body sendRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")

		return
	}

	var userID *uuid.UUID

	if body.UserID != nil && *body.UserID != "" {
		parsed, err := uuid.Parse(*body.UserID)
		if err != nil {
			httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "userId must be a valid UUID")

			return
		}

		userID = &parsed
	}

	req := comms.SendRequest{
		Channel:        comms.Channel(body.Channel),
		To:             body.To,
		TemplateID:     body.TemplateID,
		Locale:         body.Locale,
		Vars:           body.Vars,
		IdempotencyKey: body.IdempotencyKey,
		UserID:         userID,
	}

	res, err := h.svc.Send(c.Request.Context(), req)
	if err != nil {
		writeSendError(c, err)

		return
	}

	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{
		"sendId":  res.SendID,
		"status":  res.Status,
		"deduped": res.Deduped,
	}})
}

// writeSendError maps a comms service error to a stable API code WITHOUT leaking
// provider internals. All 500-class paths are logged at ERROR so the actual
// root cause is visible in the notification service logs (previously silent).
func writeSendError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, comms.ErrValidation),
		errors.Is(err, comms.ErrTemplateNotFound),
		errors.Is(err, comms.ErrMissingVar),
		errors.Is(err, comms.ErrRenderTooLarge):
		httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid send request")

	case comms.IsProviderUnavailable(err):
		// Log the real error so operators can see what is unavailable.
		slog.Error("comms send: provider unavailable", "err", err)
		// Do not echo which provider or why — just that delivery is unavailable.
		httpx.ErrCode(c, http.StatusInternalServerError, "PROVIDER_UNAVAILABLE", "delivery provider is unavailable")

	default:
		slog.Error("comms send: internal error", "err", err)
		httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

// Receipts is the (stubbed) provider delivery-receipt webhook
// POST /v1/comms/receipts/:provider. Full provider-specific authentication is a
// follow-up; until then it returns 501 and accepts nothing. It is NOT left open:
// the route is registered behind the same S2S guard as the send endpoint, so an
// unauthenticated caller never reaches this handler.
func (h *CommsHandler) Receipts(c *gin.Context) {
	httpx.ErrCode(c, http.StatusNotImplemented, "NOT_IMPLEMENTED", "receipts webhook not yet implemented")
}
