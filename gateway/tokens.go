package main

import (
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/omachala/diction/gateway/core"
)

// --- REST API token store (multi-user bearer auth) ---
//
// This is an additive, orthogonal layer to the AUTH_ENABLED subscription
// handshake (Apple JWS / trial tokens). It gates ONLY the REST OpenAI routes
// (/v1/models, /v1/audio/transcriptions). The WebSocket /v1/audio/stream and
// the native handshake are intentionally left untouched.
//
// Tokens come from two sources, merged:
//   - API_TOKENS: comma-separated "token:name" pairs, e.g. "abc:jefe,def:pote1"
//   - API_TOKENS_FILE: a mounted file, one "token:name" per line; blank lines
//     and lines starting with '#' are ignored. Reloaded at runtime when the
//     file changes on disk (hot reload).
//
// If neither is set the store is nil and the middleware is a no-op, preserving
// the exact pre-existing behavior (zero regression).

type tokenStore struct {
	mu     sync.RWMutex
	tokens map[string]string // token -> user name (env entries ∪ file entries)

	envTokens map[string]string // static, parsed once from API_TOKENS
	filePath  string            // API_TOKENS_FILE, empty if unset
	fileMod   time.Time         // last-seen file modtime, for hot reload
}

// newTokenStore builds the store from API_TOKENS and API_TOKENS_FILE.
// Returns nil (middleware inactive) when both are empty/undefined.
func newTokenStore() *tokenStore {
	raw := core.EnvOrDefault("API_TOKENS", "")
	filePath := core.EnvOrDefault("API_TOKENS_FILE", "")
	if raw == "" && filePath == "" {
		return nil
	}

	s := &tokenStore{
		tokens:    make(map[string]string),
		envTokens: parseTokenEntries(strings.Split(raw, ",")),
		filePath:  filePath,
	}
	s.reload()

	if filePath != "" {
		go s.watchFile()
	}
	return s
}

// parseTokenEntries parses a slice of "token:name" strings into a map.
// Blank entries and comment lines (starting with '#') are skipped, as are
// malformed entries (missing ':', empty token, or empty name). Tokens are
// never logged.
func parseTokenEntries(entries []string) map[string]string {
	out := make(map[string]string)
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" || strings.HasPrefix(e, "#") {
			continue
		}
		parts := strings.SplitN(e, ":", 2)
		if len(parts) != 2 {
			log.Printf("API token: skipping malformed entry (expected token:name)")
			continue
		}
		token := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		if token == "" || name == "" {
			log.Printf("API token: skipping entry with empty token or name")
			continue
		}
		out[token] = name
	}
	return out
}

// reload rebuilds the combined token map from the static env entries plus the
// current contents of API_TOKENS_FILE (if configured). Called at startup and
// whenever the file changes.
func (s *tokenStore) reload() {
	combined := make(map[string]string, len(s.envTokens))
	for t, n := range s.envTokens {
		combined[t] = n
	}

	if s.filePath != "" {
		if data, err := os.ReadFile(s.filePath); err != nil {
			log.Printf("API token: cannot read %s: %v", s.filePath, err)
		} else {
			for t, n := range parseTokenEntries(strings.Split(string(data), "\n")) {
				combined[t] = n
			}
			if info, err := os.Stat(s.filePath); err == nil {
				s.fileMod = info.ModTime()
			}
		}
	}

	s.mu.Lock()
	s.tokens = combined
	s.mu.Unlock()
}

// watchFile polls the token file's modtime and reloads on change.
// Dependency-free hot reload — good enough for a rarely-changing config file.
func (s *tokenStore) watchFile() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		info, err := os.Stat(s.filePath)
		if err != nil {
			continue
		}
		if info.ModTime().After(s.fileMod) {
			log.Printf("API token: %s changed, reloading", s.filePath)
			s.reload()
			log.Printf("API token: reloaded, %d token(s) active", s.count())
		}
	}
}

// lookup returns the user name for a token and whether it is valid.
func (s *tokenStore) lookup(token string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	name, ok := s.tokens[token]
	return name, ok
}

// count returns the number of currently-loaded tokens.
func (s *tokenStore) count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens)
}

// tokenFromRequest extracts the bearer token from a request. The Authorization
// header ("Bearer <token>") is always checked first. When allowQuery is true it
// falls back to the ?token then ?api_key query parameters — used ONLY for the
// WebSocket upgrade, where clients (the native iOS app) can't always set a
// header. REST routes pass allowQuery=false: tokens in URLs leak into access
// logs, history, and referrers, so they are never accepted there.
func tokenFromRequest(r *http.Request, allowQuery bool) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if allowQuery {
		if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
			return t
		}
		if t := strings.TrimSpace(r.URL.Query().Get("api_key")); t != "" {
			return t
		}
	}
	return ""
}

// restTokenMiddleware wraps a REST handler with bearer-token auth (header only).
// When store is nil the handler is returned unchanged (auth inactive — identical
// to prior behavior). On success it logs the associated user name; on
// missing/invalid tokens it returns HTTP 401 with a JSON body matching the
// existing error shape.
func restTokenMiddleware(next http.HandlerFunc, store *tokenStore) http.HandlerFunc {
	if store == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r, false)
		name, ok := store.lookup(token)
		if token == "" || !ok {
			writeTokenAuthError(w)
			return
		}
		log.Printf("REST auth: %q authorized for %s %s", name, r.Method, r.URL.Path)
		next(w, r)
	}
}

// wsTokenMiddleware gates the WebSocket upgrade with the SAME token store as the
// REST routes, accepting the token via header OR query param (?token / ?api_key)
// because WebSocket clients can't always set an Authorization header. It runs
// before the upgrade: a missing/invalid token returns 401 and the connection is
// never promoted to a WebSocket, so the post-upgrade native handshake is left
// untouched. store == nil → no-op (identical to prior behavior, zero regression).
func wsTokenMiddleware(next http.HandlerFunc, store *tokenStore) http.HandlerFunc {
	if store == nil {
		return next
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := tokenFromRequest(r, true)
		name, ok := store.lookup(token)
		if token == "" || !ok {
			writeTokenAuthError(w)
			return
		}
		// Security: never log the token or the query string — only the user name
		// and the path (r.URL.Path excludes the ?token=… query).
		log.Printf("WS auth: %q authorized for %s", name, r.URL.Path)
		next(w, r)
	}
}

func writeTokenAuthError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error":"invalid or missing API token"}`))
}
