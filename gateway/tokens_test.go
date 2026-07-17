package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTokenEntries(t *testing.T) {
	got := parseTokenEntries([]string{
		"abc:jefe",
		" def : pote1 ", // surrounding whitespace is trimmed
		"",              // blank skipped
		"# comment",     // comment skipped
		"nocolon",       // malformed skipped
		"empty:",        // empty name skipped
		":noname",       // empty token skipped
	})
	if len(got) != 2 {
		t.Fatalf("want 2 valid entries, got %d: %v", len(got), got)
	}
	if got["abc"] != "jefe" {
		t.Errorf("abc → %q, want jefe", got["abc"])
	}
	if got["def"] != "pote1" {
		t.Errorf("def → %q, want pote1 (whitespace not trimmed)", got["def"])
	}
}

func TestRestTokenMiddleware_InactiveWhenNil(t *testing.T) {
	called := false
	next := func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }
	h := restTokenMiddleware(next, nil)

	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(http.MethodGet, "/v1/models", nil))

	if !called || rr.Code != http.StatusOK {
		t.Fatalf("nil store must pass through unchanged: called=%v code=%d", called, rr.Code)
	}
}

func TestRestTokenMiddleware_Validation(t *testing.T) {
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte("ok")) }
	store := &tokenStore{tokens: map[string]string{"abc": "jefe"}}
	h := restTokenMiddleware(next, store)

	cases := []struct {
		name     string
		header   string
		wantCode int
	}{
		{"valid token", "Bearer abc", http.StatusOK},
		{"invalid token", "Bearer wrong", http.StatusUnauthorized},
		{"empty bearer", "Bearer ", http.StatusUnauthorized},
		{"wrong scheme", "Basic abc", http.StatusUnauthorized},
		{"absent header", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rr.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusUnauthorized {
				if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("content-type = %q, want application/json", ct)
				}
				if !strings.Contains(rr.Body.String(), `"error"`) {
					t.Errorf("body = %q, want JSON error", rr.Body.String())
				}
			}
		})
	}
}

func TestTokenFromRequest(t *testing.T) {
	// Header always wins over query.
	r := httptest.NewRequest(http.MethodGet, "/v1/audio/stream?token=qtok", nil)
	r.Header.Set("Authorization", "Bearer htok")
	if got := tokenFromRequest(r, true); got != "htok" {
		t.Errorf("header should win: got %q", got)
	}
	// ?token when no header (allowQuery).
	r = httptest.NewRequest(http.MethodGet, "/v1/audio/stream?token=qtok", nil)
	if got := tokenFromRequest(r, true); got != "qtok" {
		t.Errorf("?token: got %q", got)
	}
	// ?api_key fallback.
	r = httptest.NewRequest(http.MethodGet, "/v1/audio/stream?api_key=atok", nil)
	if got := tokenFromRequest(r, true); got != "atok" {
		t.Errorf("?api_key: got %q", got)
	}
	// ?token beats ?api_key.
	r = httptest.NewRequest(http.MethodGet, "/v1/audio/stream?token=qtok&api_key=atok", nil)
	if got := tokenFromRequest(r, true); got != "qtok" {
		t.Errorf("?token should beat ?api_key: got %q", got)
	}
	// allowQuery=false must ignore query params entirely (REST never trusts them).
	r = httptest.NewRequest(http.MethodGet, "/v1/models?token=qtok&api_key=atok", nil)
	if got := tokenFromRequest(r, false); got != "" {
		t.Errorf("REST must ignore query token, got %q", got)
	}
}

func TestWsTokenMiddleware(t *testing.T) {
	next := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte("upgraded")) }

	// nil store → passthrough (WS stays open, zero regression).
	rr := httptest.NewRecorder()
	wsTokenMiddleware(next, nil)(rr, httptest.NewRequest(http.MethodGet, "/v1/audio/stream", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("nil store must pass through, got %d", rr.Code)
	}

	store := &tokenStore{tokens: map[string]string{"abc": "jefe"}}
	h := wsTokenMiddleware(next, store)

	cases := []struct {
		name     string
		url      string
		header   string
		wantCode int
	}{
		{"no token", "/v1/audio/stream", "", http.StatusUnauthorized},
		{"good query token", "/v1/audio/stream?token=abc", "", http.StatusOK},
		{"bad query token", "/v1/audio/stream?token=wrong", "", http.StatusUnauthorized},
		{"good api_key", "/v1/audio/stream?api_key=abc", "", http.StatusOK},
		{"good header", "/v1/audio/stream", "Bearer abc", http.StatusOK},
		{"bad header", "/v1/audio/stream", "Bearer wrong", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rr := httptest.NewRecorder()
			h(rr, req)
			if rr.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rr.Code, tc.wantCode)
			}
			if tc.wantCode == http.StatusUnauthorized {
				if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("content-type = %q, want application/json", ct)
				}
				if !strings.Contains(rr.Body.String(), `"error"`) {
					t.Errorf("401 body = %q, want JSON error", rr.Body.String())
				}
			}
		})
	}
}

func TestNewTokenStore_InactiveWhenUnset(t *testing.T) {
	t.Setenv("API_TOKENS", "")
	t.Setenv("API_TOKENS_FILE", "")
	if s := newTokenStore(); s != nil {
		t.Fatalf("want nil store when both env vars unset, got %+v", s)
	}
}

func TestNewTokenStore_FromEnv(t *testing.T) {
	t.Setenv("API_TOKENS", "abc:jefe,def:pote1")
	t.Setenv("API_TOKENS_FILE", "")
	s := newTokenStore()
	if s == nil {
		t.Fatal("want non-nil store")
	}
	if s.count() != 2 {
		t.Fatalf("count = %d, want 2", s.count())
	}
	if n, ok := s.lookup("abc"); !ok || n != "jefe" {
		t.Errorf("lookup(abc) = %q,%v", n, ok)
	}
}

func TestTokenStore_HotReload(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tokens.txt")
	if err := os.WriteFile(f, []byte("tok1:alice\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_TOKENS", "")
	t.Setenv("API_TOKENS_FILE", f)

	s := newTokenStore()
	if s == nil {
		t.Fatal("want non-nil store")
	}
	if _, ok := s.lookup("tok1"); !ok {
		t.Fatal("tok1 should be present initially")
	}

	// Rewrite the file with a different token, then reload (the watcher calls
	// this same path on mtime change).
	if err := os.WriteFile(f, []byte("tok2:bob\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s.reload()

	if _, ok := s.lookup("tok2"); !ok {
		t.Error("tok2 should be present after reload")
	}
	if _, ok := s.lookup("tok1"); ok {
		t.Error("tok1 should be gone after reload")
	}
}

func TestNewTokenStore_FromFileAndEnvMerged(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "tokens.txt")
	if err := os.WriteFile(f, []byte("# team tokens\n\nfiletok:alice\nbadline\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("API_TOKENS", "envtok:bob")
	t.Setenv("API_TOKENS_FILE", f)

	s := newTokenStore()
	if s == nil {
		t.Fatal("want non-nil store")
	}
	if s.count() != 2 {
		t.Fatalf("count = %d, want 2 (envtok + filetok)", s.count())
	}
	if n, ok := s.lookup("filetok"); !ok || n != "alice" {
		t.Errorf("file token lookup = %q,%v", n, ok)
	}
	if n, ok := s.lookup("envtok"); !ok || n != "bob" {
		t.Errorf("env token lookup = %q,%v", n, ok)
	}
}
