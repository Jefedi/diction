package core

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnvDurationOrDefault(t *testing.T) {
	if got := EnvDurationOrDefault("DICTION_TEST_DUR_UNSET", 5*time.Second); got != 5*time.Second {
		t.Errorf("unset: got %v, want 5s", got)
	}
	t.Setenv("DICTION_TEST_DUR", "250ms")
	if got := EnvDurationOrDefault("DICTION_TEST_DUR", time.Second); got != 250*time.Millisecond {
		t.Errorf("valid: got %v, want 250ms", got)
	}
	t.Setenv("DICTION_TEST_DUR", "notaduration")
	if got := EnvDurationOrDefault("DICTION_TEST_DUR", 7*time.Second); got != 7*time.Second {
		t.Errorf("invalid: got %v, want fallback 7s", got)
	}
}

// TestTranscription_RetriesOnTransientTransportError simulates a stale
// keep-alive socket: the first backend connection is hijacked and closed with
// no response (a transport error, not an HTTP 5xx), the second succeeds.
// With BackendMaxRetries=1 the gateway retries on a fresh connection and the
// client sees 200 — the direct fix for the "every other request fails" symptom.
func TestTranscription_RetriesOnTransientTransportError(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("server does not support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close() // abrupt close → client sees a transport error
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok after retry"}`)
	}))
	defer srv.Close()

	g := &Gateway{
		backends:          []Backend{{Name: "primary", URL: srv.URL, Aliases: []string{"primary"}}},
		health:            newHealthState(),
		defaultModel:      "primary",
		maxBodySize:       10 * 1024 * 1024,
		backendMaxRetries: 1,
	}
	g.health.set("primary", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200 after transport retry, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("backend hits: want 2, got %d", got)
	}
	if !strings.Contains(rr.Body.String(), "ok after retry") {
		t.Errorf("body: want retry response, got %q", rr.Body.String())
	}
}

// TestTranscription_TransportError_JSON502 verifies that a hard transport
// failure with no retry and no fallback backend still returns a clean JSON
// body (not an empty 502).
func TestTranscription_TransportError_JSON502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("no hijacker")
			return
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer srv.Close()

	g := &Gateway{
		backends:          []Backend{{Name: "primary", URL: srv.URL, Aliases: []string{"primary"}}},
		health:            newHealthState(),
		defaultModel:      "primary",
		maxBodySize:       10 * 1024 * 1024,
		backendMaxRetries: 0, // no retry
	}
	g.health.set("primary", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"error"`) {
		t.Errorf("body: want JSON error, got %q", rr.Body.String())
	}
}

// TestTranscription_BackendTimeout_JSON504 verifies that a backend slower than
// BackendTimeout is cut off and the client gets a clean 504 JSON error.
func TestTranscription_BackendTimeout_JSON504(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
			fmt.Fprint(w, `{"text":"too late"}`)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	g := &Gateway{
		backends:       []Backend{{Name: "primary", URL: srv.URL, Aliases: []string{"primary"}}},
		health:         newHealthState(),
		defaultModel:   "primary",
		maxBodySize:    10 * 1024 * 1024,
		backendTimeout: 100 * time.Millisecond,
	}
	g.health.set("primary", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
	req.Header.Set("Content-Type", ct)
	rr := httptest.NewRecorder()
	g.TranscriptionHandler()(rr, req)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status: want 504 on backend timeout, got %d (body: %s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"error"`) {
		t.Errorf("body: want JSON error, got %q", rr.Body.String())
	}
}

// TestTranscription_ConcurrencyLimit verifies BackendMaxConcurrent=1 serializes
// requests: with 4 concurrent clients, the backend never sees more than 1
// in-flight request at a time.
func TestTranscription_ConcurrencyLimit(t *testing.T) {
	var inflight, maxInflight int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			m := atomic.LoadInt32(&maxInflight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInflight, m, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer srv.Close()

	g := &Gateway{
		backends:     []Backend{{Name: "primary", URL: srv.URL, Aliases: []string{"primary"}}},
		health:       newHealthState(),
		defaultModel: "primary",
		maxBodySize:  10 * 1024 * 1024,
		backendSem:   make(chan struct{}, 1),
	}
	g.health.set("primary", true)

	// Build the request body once — buildMultipart may call t.Fatal, which is
	// only safe on the test goroutine.
	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
			req.Header.Set("Content-Type", ct)
			rr := httptest.NewRecorder()
			g.TranscriptionHandler()(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("status: want 200, got %d", rr.Code)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInflight); got != 1 {
		t.Errorf("max concurrent backend requests: want 1 with sem=1, got %d", got)
	}
}

// TestTranscription_NoConcurrencyLimitByDefault confirms the default (nil sem)
// leaves concurrency unbounded — no regression for existing deployments.
func TestTranscription_NoConcurrencyLimitByDefault(t *testing.T) {
	var maxInflight, inflight int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inflight, 1)
		for {
			m := atomic.LoadInt32(&maxInflight)
			if cur <= m || atomic.CompareAndSwapInt32(&maxInflight, m, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"text":"ok"}`)
	}))
	defer srv.Close()

	g := &Gateway{
		backends:     []Backend{{Name: "primary", URL: srv.URL, Aliases: []string{"primary"}}},
		health:       newHealthState(),
		defaultModel: "primary",
		maxBodySize:  10 * 1024 * 1024,
		// backendSem nil → unlimited
	}
	g.health.set("primary", true)

	body, ct := buildMultipart(t, map[string]string{"language": "en"}, "audio.m4a", "fake-audio")

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", bytes.NewReader(body))
			req.Header.Set("Content-Type", ct)
			rr := httptest.NewRecorder()
			g.TranscriptionHandler()(rr, req)
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&maxInflight); got < 2 {
		t.Errorf("expected concurrent backend requests (>=2) with no limit, got %d", got)
	}
}
