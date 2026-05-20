package codexquota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type fakeManager struct {
	auths  []*cliproxyauth.Auth
	calls  []fakeCall
	failID string
}

type fakeCall struct {
	authID string
	model  string
}

func (m *fakeManager) List() []*cliproxyauth.Auth {
	return m.auths
}

func (m *fakeManager) Execute(ctx context.Context, providers []string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	authID, _ := opts.Metadata[cliproxyexecutor.PinnedAuthMetadataKey].(string)
	m.calls = append(m.calls, fakeCall{authID: authID, model: req.Model})
	if authID == m.failID {
		return cliproxyexecutor.Response{}, context.Canceled
	}
	return cliproxyexecutor.Response{Payload: []byte(`{"ok":true}`)}, nil
}

func TestLoadPromptsIgnoresBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.txt")
	if err := os.WriteFile(path, []byte("\nfirst\n  \nsecond \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prompts, err := LoadPrompts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 || prompts[0] != "first" || prompts[1] != "second" {
		t.Fatalf("unexpected prompts: %#v", prompts)
	}
}

func TestActiveCodexAuthsFiltersAndSorts(t *testing.T) {
	auths := []*cliproxyauth.Auth{
		{ID: "b", Provider: "codex", FileName: "b.json", Status: cliproxyauth.StatusActive},
		{ID: "disabled", Provider: "codex", FileName: "a.json", Status: cliproxyauth.StatusActive, Disabled: true},
		{ID: "gemini", Provider: "gemini", FileName: "c.json", Status: cliproxyauth.StatusActive},
		{ID: "error", Provider: "codex", FileName: "d.json", Status: cliproxyauth.StatusError},
		{ID: "a", Provider: "codex", FileName: "a.json", Status: cliproxyauth.StatusActive},
	}
	got := ActiveCodexAuths(auths)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("order = %s,%s; want a,b", got[0].ID, got[1].ID)
	}
}

func TestRunnerPinsEachActiveCodexAuthAndSleepsBetweenAuths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompts.txt")
	if err := os.WriteFile(path, []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &fakeManager{auths: []*cliproxyauth.Auth{
		{ID: "b", Provider: "codex", FileName: "b.json", Status: cliproxyauth.StatusActive},
		{ID: "a", Provider: "codex", FileName: "a.json", Status: cliproxyauth.StatusActive},
	}}
	var sleeps []time.Duration
	runner := NewRunner(
		manager,
		WithPromptPath(path),
		WithInterval(func() time.Duration { return 30 * time.Second }),
		WithSleep(func(ctx context.Context, d time.Duration) error {
			sleeps = append(sleeps, d)
			return nil
		}),
	)
	var events []Event
	summary, err := runner.TryRun(context.Background(), func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 2 || summary.Success != 2 || summary.Failed != 0 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(manager.calls) != 2 || manager.calls[0].authID != "a" || manager.calls[1].authID != "b" {
		t.Fatalf("calls = %+v", manager.calls)
	}
	if len(sleeps) != 1 || sleeps[0] != 30*time.Second {
		t.Fatalf("sleeps = %+v", sleeps)
	}
	if len(events) != 2 || events[0].Type != "result" || !json.Valid(events[0].Response) {
		t.Fatalf("events = %+v", events)
	}
}

func TestRandomIntervalIsThirtyToSixtySeconds(t *testing.T) {
	for i := 0; i < 100; i++ {
		d := randomInterval()
		if d < 30*time.Second || d > 60*time.Second {
			t.Fatalf("interval %s outside range", d)
		}
	}
}
