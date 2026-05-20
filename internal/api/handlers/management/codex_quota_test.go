package management

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/codexquota"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRunCodexQuotaStreamsErrorAndDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "prompts.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(codexquota.PromptPathEnv, path)

	router := gin.New()
	h := &Handler{authManager: coreauth.NewManager(nil, nil, nil)}
	router.POST("/v0/management/codex-quota/run", h.RunCodexQuota)

	req := httptest.NewRequest(http.MethodPost, "/v0/management/codex-quota/run", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: error") || !strings.Contains(body, "event: done") {
		t.Fatalf("expected error and done SSE events, got %q", body)
	}
	if !strings.Contains(body, codexquota.ErrNoAuths.Error()) {
		t.Fatalf("expected no auth error, got %q", body)
	}
}
