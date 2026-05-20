package management

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/codexquota"
)

// RunCodexQuota manually starts the Codex quota warmer and streams per-auth results.
func (h *Handler) RunCodexQuota(c *gin.Context) {
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	runner := codexquota.NewRunner(h.authManager)
	summary, err := runner.TryRun(c.Request.Context(), func(event codexquota.Event) error {
		if errWrite := writeCodexQuotaSSE(c.Writer, event.Type, event); errWrite != nil {
			return errWrite
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !errors.Is(err, c.Request.Context().Err()) {
		_ = writeCodexQuotaSSE(c.Writer, "error", codexquota.Event{Type: "error", Error: err.Error()})
		flusher.Flush()
	}
	if c.Request.Context().Err() == nil {
		_ = writeCodexQuotaSSE(c.Writer, "done", summary)
		flusher.Flush()
	}
}

func writeCodexQuotaSSE(w gin.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(w, "event: %s\n", event); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", data)
	return err
}
