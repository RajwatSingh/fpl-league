package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"fplleague/fpl"
)

//go:embed web
var webFiles embed.FS

// cacheTTL keeps repeated page loads off the FPL API. A league report is
// dozens of upstream calls, so refetching it on every browser refresh would be
// both slow and rude.
const cacheTTL = 60 * time.Second

// hardMaxManagers caps the fan-out no matter what the query string asks for.
// Without it a public deployment is an open proxy: ?league=314&max=0 would walk
// eleven million managers, two upstream calls each, from this server's IP.
const hardMaxManagers = 100

// allowedLeagues, when non-empty, is the only set of leagues this server will
// fetch. Set ALLOWED_LEAGUES=580906,123456 to lock a public deployment down.
func allowedLeagues() map[int]bool {
	raw := os.Getenv("ALLOWED_LEAGUES")
	if raw == "" {
		return nil
	}
	out := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		if id, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			out[id] = true
		}
	}
	return out
}

type cacheEntry struct {
	report *fpl.Report
	stored time.Time
}

type server struct {
	client *fpl.Client
	mu     sync.Mutex
	cache  map[string]cacheEntry
}

func serve(addr string, concurrency int) error {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}

	s := &server{client: fpl.NewClient(), cache: map[string]cacheEntry{}}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/report", s.handleReport(concurrency))

	log.Printf("fpl-league-rank listening on http://localhost%s", addr)
	return http.ListenAndServe(addr, mux)
}

func (s *server) handleReport(concurrency int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		leagueID, err := intParam(r, "league", defaultLeague)
		if err != nil || leagueID <= 0 {
			writeErr(w, http.StatusBadRequest, "a numeric league id is required")
			return
		}
		if allow := allowedLeagues(); allow != nil && !allow[leagueID] {
			writeErr(w, http.StatusForbidden,
				fmt.Sprintf("league %d is not on this deployment's allowlist", leagueID))
			return
		}

		gw, _ := intParam(r, "gw", 0)
		max, _ := intParam(r, "max", 0)
		if max <= 0 || max > hardMaxManagers {
			max = hardMaxManagers
		}

		// A shared CDN cache in front of this is what makes a public deployment
		// viable: the in-memory cache below is per-instance and dies with it,
		// but s-maxage is shared, and stale-while-revalidate means a visitor
		// during a refresh gets last minute's numbers instantly instead of
		// waiting on fifty upstream calls.
		w.Header().Set("Cache-Control", "public, s-maxage=60, stale-while-revalidate=300")

		key := fmt.Sprintf("%d/%d/%d", leagueID, gw, max)
		if rep, ok := s.cached(key); ok {
			writeJSON(w, rep)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()

		rep, err := s.client.BuildReport(ctx, leagueID, fpl.Options{
			GW:          gw,
			MaxManagers: max,
			Concurrency: concurrency,
			WithDetail:  true,
		})
		if err != nil {
			// The browser needs the real reason: a wrong league id and a rate
			// limit call for completely different fixes by the person reading.
			status := http.StatusBadGateway
			msg := err.Error()
			if fpl.NotFound(err) {
				status = http.StatusNotFound
				msg = fmt.Sprintf("no league with id %d, or it is private", leagueID)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				msg = "the FPL API took too long — try a smaller -max or retry"
			}
			writeErr(w, status, msg)
			return
		}

		s.store(key, rep)
		writeJSON(w, rep)
	}
}

func (s *server) cached(key string) (*fpl.Report, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.cache[key]
	if !ok || time.Since(e.stored) > cacheTTL {
		return nil, false
	}
	return e.report, true
}

func (s *server) store(key string, rep *fpl.Report) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[key] = cacheEntry{report: rep, stored: time.Now()}
}

func intParam(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	return strconv.Atoi(v)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encoding response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
