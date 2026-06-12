package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/CoverOnes/notification/internal/domain"
	"github.com/CoverOnes/notification/internal/platform/httpx"
	"github.com/CoverOnes/notification/internal/store"
	"github.com/gin-gonic/gin"
)

// maxWaitlistBody caps the POST /v1/waitlist request body size (DoS defense —
// backend-security-design §io.LimitReader requirement).
const maxWaitlistBody = 8 * 1024

// WaitlistHandler handles the public waitlist capture endpoint.
type WaitlistHandler struct {
	store store.WaitlistStore
}

// NewWaitlistHandler creates a WaitlistHandler.
func NewWaitlistHandler(s store.WaitlistStore) *WaitlistHandler {
	return &WaitlistHandler{store: s}
}

// captureRequest is the POST /v1/waitlist request body.
type captureRequest struct {
	Email        string `json:"email"`
	Company      string `json:"company"`
	InterestedIn string `json:"interestedIn"`
}

// Capture handles POST /v1/waitlist.
// This endpoint is PUBLIC — no auth middleware is applied. It must be registered
// OUTSIDE any authenticated route group (see router.go).
//
// Privacy: responds with the same 202 {ok:true} whether the email is new or
// already on the waitlist, to prevent email enumeration.
func (h *WaitlistHandler) Capture(c *gin.Context) {
	// Bound the body before decoding (DoS defense — backend-security-design).
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWaitlistBody)

	var body captureRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")

		return
	}

	// Source is derived from a trusted field (not user-supplied).
	// The handler currently hardcodes "web-form"; a future version may
	// accept it from a validated server-side routing attribute.
	entry, err := domain.NewWaitlistEntry(body.Email, body.Company, body.InterestedIn, "web-form")
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrWaitlistInvalidEmail):
			httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "invalid email address")
		case errors.Is(err, domain.ErrWaitlistInvalidInput):
			httpx.ErrCode(c, http.StatusBadRequest, "VALIDATION_ERROR", "input contains disallowed characters or exceeds length limit")
		default:
			slog.Error("waitlist entry validation", "err", err)
			httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
		}

		return
	}

	if _, storeErr := h.store.AddToWaitlist(c.Request.Context(), entry); storeErr != nil {
		slog.Error("add to waitlist", "err", storeErr)
		httpx.ErrCode(c, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")

		return
	}

	// Always respond 202 regardless of whether the entry was new or duplicate —
	// do NOT leak "already on waitlist" (privacy / enumeration prevention).
	c.JSON(http.StatusAccepted, gin.H{"data": gin.H{"ok": true}})
}
