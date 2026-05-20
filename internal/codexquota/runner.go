// Package codexquota runs periodic Codex quota warming requests.
package codexquota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

const (
	DefaultPromptPath = "./codex-quota-prompts.txt"
	DefaultModel      = "gpt-5.3-codex"

	PromptPathEnv = "CLI_PROXY_CODEX_QUOTA_PROMPTS_PATH"
)

var (
	ErrAlreadyRunning = errors.New("codex quota warmer already running")
	ErrNoPrompts      = errors.New("codex quota warmer prompt file has no prompts")
	ErrNoAuths        = errors.New("codex quota warmer has no active codex auth files")

	globalRunMu sync.Mutex
)

// AuthManager is the subset of the core auth manager used by the quota warmer.
type AuthManager interface {
	List() []*cliproxyauth.Auth
	Execute(context.Context, []string, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error)
}

// EventSink receives per-auth execution updates.
type EventSink func(Event) error

// Event describes one SSE/loggable quota warmer update.
type Event struct {
	Type      string          `json:"-"`
	AuthID    string          `json:"auth_id,omitempty"`
	AuthFile  string          `json:"auth_file,omitempty"`
	AuthIndex string          `json:"auth_index,omitempty"`
	Label     string          `json:"label,omitempty"`
	Prompt    string          `json:"prompt,omitempty"`
	Response  json.RawMessage `json:"response,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// Summary captures the outcome of one warmer run.
type Summary struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
}

// Runner executes quota warmer requests.
type Runner struct {
	manager    AuthManager
	promptPath string
	model      string
	sleep      func(context.Context, time.Duration) error
	interval   func() time.Duration

	runMu  *sync.Mutex
	randMu sync.Mutex
	rand   *rand.Rand
}

// Option customises a Runner.
type Option func(*Runner)

// WithPromptPath overrides the prompt file path.
func WithPromptPath(path string) Option {
	return func(r *Runner) {
		if strings.TrimSpace(path) != "" {
			r.promptPath = strings.TrimSpace(path)
		}
	}
}

// WithSleep overrides inter-auth sleeping. It is intended for tests.
func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(r *Runner) {
		if sleep != nil {
			r.sleep = sleep
		}
	}
}

// WithInterval overrides inter-auth interval generation. It is intended for tests.
func WithInterval(interval func() time.Duration) Option {
	return func(r *Runner) {
		if interval != nil {
			r.interval = interval
		}
	}
}

// WithRand overrides prompt randomisation. It is intended for tests.
func WithRand(src *rand.Rand) Option {
	return func(r *Runner) {
		if src != nil {
			r.rand = src
		}
	}
}

// NewRunner creates a quota warmer runner.
func NewRunner(manager AuthManager, opts ...Option) *Runner {
	r := &Runner{
		manager:    manager,
		promptPath: ResolvePromptPath(),
		model:      DefaultModel,
		sleep:      sleepContext,
		interval:   randomInterval,
		rand:       rand.New(rand.NewSource(time.Now().UnixNano())),
		runMu:      &globalRunMu,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ResolvePromptPath returns the configured prompt file path.
func ResolvePromptPath() string {
	if path := strings.TrimSpace(os.Getenv(PromptPathEnv)); path != "" {
		return path
	}
	return DefaultPromptPath
}

// ActiveCodexAuths filters and sorts active Codex auth files.
func ActiveCodexAuths(auths []*cliproxyauth.Auth) []*cliproxyauth.Auth {
	out := make([]*cliproxyauth.Auth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil || auth.Disabled {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		if auth.Status != cliproxyauth.StatusActive {
			continue
		}
		out = append(out, auth)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return authSortKey(out[i]) < authSortKey(out[j])
	})
	return out
}

// LoadPrompts reads non-empty prompt lines from path.
func LoadPrompts(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read codex quota prompts: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	prompts := make([]string, 0, len(lines))
	for _, line := range lines {
		prompt := strings.TrimSpace(line)
		if prompt != "" {
			prompts = append(prompts, prompt)
		}
	}
	if len(prompts) == 0 {
		return nil, ErrNoPrompts
	}
	return prompts, nil
}

// TryRun executes one warmer run unless another run is active.
func (r *Runner) TryRun(ctx context.Context, sink EventSink) (Summary, error) {
	if r == nil {
		return Summary{}, ErrNoAuths
	}
	if r.runMu == nil {
		r.runMu = &globalRunMu
	}
	if !r.runMu.TryLock() {
		return Summary{}, ErrAlreadyRunning
	}
	defer r.runMu.Unlock()
	return r.runLocked(ctx, sink)
}

func (r *Runner) runLocked(ctx context.Context, sink EventSink) (Summary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prompts, err := LoadPrompts(r.promptPath)
	if err != nil {
		return Summary{}, err
	}
	if r.manager == nil {
		return Summary{}, ErrNoAuths
	}
	auths := ActiveCodexAuths(r.manager.List())
	if len(auths) == 0 {
		return Summary{}, ErrNoAuths
	}

	summary := Summary{Total: len(auths)}
	for i, auth := range auths {
		prompt := r.pickPrompt(prompts)
		event := eventForAuth("result", auth, prompt)
		resp, errExec := r.executeOne(ctx, auth, prompt)
		if errExec != nil {
			summary.Failed++
			event.Type = "error"
			event.Error = errExec.Error()
		} else {
			summary.Success++
			event.Response = json.RawMessage(resp.Payload)
		}
		if sink != nil {
			if errSink := sink(event); errSink != nil {
				return summary, errSink
			}
		}
		if i < len(auths)-1 {
			if errSleep := r.sleep(ctx, r.interval()); errSleep != nil {
				return summary, errSleep
			}
		}
	}
	return summary, nil
}

func (r *Runner) executeOne(ctx context.Context, auth *cliproxyauth.Auth, prompt string) (cliproxyexecutor.Response, error) {
	payload, err := json.Marshal(map[string]any{
		"model":  r.model,
		"input":  prompt,
		"stream": false,
	})
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}
	req := cliproxyexecutor.Request{
		Model:   r.model,
		Payload: payload,
		Format:  sdktranslator.FormatOpenAIResponse,
	}
	opts := cliproxyexecutor.Options{
		Stream:          false,
		OriginalRequest: payload,
		SourceFormat:    sdktranslator.FormatOpenAIResponse,
		Metadata: map[string]any{
			cliproxyexecutor.PinnedAuthMetadataKey:     auth.ID,
			cliproxyexecutor.RequestedModelMetadataKey: r.model,
		},
	}
	return r.manager.Execute(ctx, []string{"codex"}, req, opts)
}

func (r *Runner) pickPrompt(prompts []string) string {
	if len(prompts) == 1 {
		return prompts[0]
	}
	r.randMu.Lock()
	idx := r.rand.Intn(len(prompts))
	r.randMu.Unlock()
	return prompts[idx]
}

func eventForAuth(eventType string, auth *cliproxyauth.Auth, prompt string) Event {
	if auth == nil {
		return Event{Type: eventType, Prompt: prompt}
	}
	auth.EnsureIndex()
	return Event{
		Type:      eventType,
		AuthID:    strings.TrimSpace(auth.ID),
		AuthFile:  authSortKey(auth),
		AuthIndex: strings.TrimSpace(auth.Index),
		Label:     strings.TrimSpace(auth.Label),
		Prompt:    prompt,
	}
}

func authSortKey(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if v := strings.TrimSpace(auth.FileName); v != "" {
		return v
	}
	if auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["path"]); v != "" {
			return v
		}
	}
	return strings.TrimSpace(auth.ID)
}

func randomInterval() time.Duration {
	return time.Duration(30+rand.Intn(31)) * time.Second
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
