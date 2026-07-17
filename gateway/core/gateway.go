package core

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

func EnvFloatOrDefault(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}

// EnvDurationOrDefault parses a Go duration string (e.g. "120s", "5s", "2m").
// Invalid or unset values fall back to the default.
func EnvDurationOrDefault(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// defaultStreamIdleTimeout is the fallback inter-frame gap for WebSocket
// audio streams. 45s is generous: healthy streams send frames every ~100ms.
const defaultStreamIdleTimeout = 45 * time.Second

// Config holds all gateway configuration.
type Config struct {
	Backends      []Backend
	DefaultModel  string
	FallbackModel string
	EnglishModel  string
	ParakeetModel string
	CohereModel   string
	MaxBodySize   int64

	// StreamIdleTimeout bounds the gap between successive WebSocket frames on
	// /v1/audio/stream. Zero → falls back to defaultStreamIdleTimeout.
	// Healthy streams send an audio frame every ~100ms, so 45s is generous.
	StreamIdleTimeout time.Duration

	// --- REST backend robustness (see proxy.go) ---
	//
	// BackendTimeout bounds a single REST transcription request to the STT
	// backend (including time queued behind BackendMaxConcurrent). Zero → no
	// explicit deadline (only the transport's ResponseHeaderTimeout applies).
	BackendTimeout time.Duration

	// BackendIdleTimeout is the idle-connection timeout of the STT transport
	// pool. Keeping it below the backend's own keep-alive timeout avoids reusing
	// a socket the backend already closed (the classic "every other request
	// fails" symptom with faster-whisper/uvicorn). Zero → 90s (legacy default).
	BackendIdleTimeout time.Duration

	// BackendMaxRetries is the number of extra attempts on the SAME backend when
	// a request fails with a transient transport error (EOF/reset/refused, e.g.
	// a stale keep-alive socket). Does not retry on timeout or client cancel.
	// Zero → no transport-level retry.
	BackendMaxRetries int

	// BackendMaxConcurrent caps simultaneous in-flight requests to the STT
	// backend. Zero → unlimited (legacy behavior). Set to 1 for single-worker
	// CPU backends like faster-whisper-medium int8.
	BackendMaxConcurrent int

	// ProfileStore enables per-device language history for auto-detect routing.
	// Nil in community builds without MariaDB — auto-detect always falls back to whisper_safe.
	ProfileStore *ProfileStore
}

// Gateway holds runtime state: backends, health, config.
type Gateway struct {
	backends      []Backend
	health        *healthState
	defaultModel  string
	fallbackModel string
	englishModel  string
	parakeetModel string
	cohereModel   string
	maxBodySize   int64
	profileStore  *ProfileStore

	// streamIdleTimeout bounds inter-frame gap on /v1/audio/stream. See Config.
	// Tests override the field directly after construction.
	streamIdleTimeout time.Duration

	// REST backend robustness knobs. See Config for semantics.
	// Tests may set these directly after constructing a Gateway literal.
	transport         http.RoundTripper // nil → package-level sttBackendTransport
	backendTimeout    time.Duration     // 0 → no explicit deadline
	backendMaxRetries int               // 0 → no transient transport retry
	backendSem        chan struct{}     // nil → unlimited concurrency

	// OnTranscription is an optional hook called after each successful transcription.
	// model is the backend name, whisperMs is inference latency, chars is transcript length,
	// durationMs is audio duration parsed from the WAV header (0 if unavailable).
	// enhance and e2e indicate whether LLM post-processing and E2E encryption were requested.
	// Leave nil in community builds.
	OnTranscription func(ctx context.Context, model string, whisperMs int64, chars int, durationMs int64, enhance, e2e bool)

	// DeviceHashFromContext returns the SHA-256 hex device hash for the current request.
	// Wired by the private gateway main() to read from the request log entry; nil in community builds.
	DeviceHashFromContext func(ctx context.Context) string

	// OnAutoDetect is called after the auto-detect routing decision (tier) and optionally again
	// when the detected language arrives from verbose_json. Either tier or lang may be empty.
	// Wired by the private gateway main() to write fields into the request log entry.
	OnAutoDetect func(ctx context.Context, tier, lang string)
}

// NewGateway creates a Gateway and starts the background health checker.
// If CUSTOM_BACKEND_URL is set, the custom backend is prepended and becomes the default.
func NewGateway(cfg Config) *Gateway {
	backends := cfg.Backends
	defaultModel := cfg.DefaultModel
	if custom := CustomBackendFromEnv(); custom != nil {
		backends = append([]Backend{*custom}, backends...)
		defaultModel = "custom"
	}
	idle := cfg.StreamIdleTimeout
	if idle <= 0 {
		idle = defaultStreamIdleTimeout
	}

	// Build the STT backend transport. IdleConnTimeout is kept short by default
	// (see Config.BackendIdleTimeout) so we never reuse a keep-alive socket the
	// backend has already closed. Other params mirror the package default.
	backendIdle := cfg.BackendIdleTimeout
	if backendIdle <= 0 {
		backendIdle = 90 * time.Second
	}
	transport := &http.Transport{
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   5,
		IdleConnTimeout:       backendIdle,
		ResponseHeaderTimeout: 10 * time.Minute,
	}

	var sem chan struct{}
	if cfg.BackendMaxConcurrent > 0 {
		sem = make(chan struct{}, cfg.BackendMaxConcurrent)
	}

	g := &Gateway{
		backends:          backends,
		health:            newHealthState(),
		defaultModel:      defaultModel,
		fallbackModel:     cfg.FallbackModel,
		englishModel:      cfg.EnglishModel,
		parakeetModel:     cfg.ParakeetModel,
		cohereModel:       cfg.CohereModel,
		maxBodySize:       cfg.MaxBodySize,
		streamIdleTimeout: idle,
		profileStore:      cfg.ProfileStore,
		transport:         transport,
		backendTimeout:    cfg.BackendTimeout,
		backendMaxRetries: cfg.BackendMaxRetries,
		backendSem:        sem,
	}
	g.startHealthChecker()
	return g
}

// resolveBackend maps a model name/alias to a backend URL and its config.
func (g *Gateway) resolveBackend(model string) (*url.URL, *Backend) {
	model = strings.TrimSpace(model)
	for i := range g.backends {
		for _, alias := range g.backends[i].Aliases {
			if strings.EqualFold(model, alias) {
				u, err := url.Parse(g.backends[i].URL)
				if err != nil {
					return nil, nil
				}
				return u, &g.backends[i]
			}
		}
	}
	return nil, nil
}

// HealthHandler returns the handler for GET /health.
func (g *Gateway) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	}
}

// CatchAllHandler returns the root / 404 handler.
func (g *Gateway) CatchAllHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"service":"diction-gateway","docs":"https://diction.one"}`))
	}
}

// --- Environment helpers ---

func EnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func EnvIntOrDefault(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func EnvBoolOrDefault(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}
