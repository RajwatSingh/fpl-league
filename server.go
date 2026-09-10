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

// The player board is built from bootstrap-static and the fixture list,
// neither of which changes more than a few times a day, so it holds for
// far longer than a league report.
const boardTTL = 15 * time.Minute

// defaultHorizon is the "next 10 fixtures" the players section is built
// around; maxHorizon stops a query string asking for the rest of the
// season and getting a response nobody can read.
const (
	defaultHorizon = 10
	maxHorizon     = 15
)

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

type teamCacheEntry struct {
	detail *fpl.TeamDetail
	stored time.Time
}

type boardCacheEntry struct {
	board  *fpl.PlayerBoard
	stored time.Time
}

type server struct {
	client     *fpl.Client
	mu         sync.Mutex
	cache      map[string]cacheEntry
	teamMu     sync.Mutex
	teamCache  map[string]teamCacheEntry
	boardMu    sync.Mutex
	boardCache map[string]boardCacheEntry
}

func serve(addr string, concurrency int) error {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		return err
	}

	s := &server{
		client:     fpl.NewClient(),
		cache:      map[string]cacheEntry{},
		teamCache:  map[string]teamCacheEntry{},
		boardCache: map[string]boardCacheEntry{},
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(sub)))
	mux.HandleFunc("/api/report", s.handleReport(concurrency))
	mux.HandleFunc("/api/team", s.handleTeam())
	mux.HandleFunc("/api/players", s.handlePlayers())

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

// handleTeam serves one manager's squad for one gameweek - the data behind
// the Team, Captain and Remaining-to-play options under a manager's name.
// It is looked up by entry id directly rather than through a league, which
// matches the underlying FPL API: an entry's picks are public regardless of
// which league you found them through.
func (s *server) handleTeam() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entry, err := intParam(r, "entry", 0)
		if err != nil || entry <= 0 {
			writeErr(w, http.StatusBadRequest, "a numeric entry id is required")
			return
		}
		gw, err := intParam(r, "gw", 0)
		if err != nil || gw <= 0 {
			writeErr(w, http.StatusBadRequest, "a numeric gw is required")
			return
		}

		w.Header().Set("Cache-Control", "public, s-maxage=60, stale-while-revalidate=300")

		key := fmt.Sprintf("%d/%d", entry, gw)
		if det, ok := s.teamCached(key); ok {
			writeJSON(w, det)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		det, err := s.client.BuildTeamDetail(ctx, entry, gw)
		if err != nil {
			status := http.StatusBadGateway
			msg := err.Error()
			if fpl.NotFound(err) {
				status = http.StatusNotFound
				msg = "no picks for that manager and gameweek - it may not have kicked off yet"
			}
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				msg = "the FPL API took too long — retry"
			}
			writeErr(w, status, msg)
			return
		}

		s.storeTeam(key, det)
		writeJSON(w, det)
	}
}

// handlePlayers serves the player board: every club's next few gameweeks
// rated for difficulty, plus a season line for every player. Unlike the
// league endpoints this one takes no league id - it is the same answer for
// everybody, which is why it can be cached far harder than a report.
func (s *server) handlePlayers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		horizon, err := intParam(r, "horizon", defaultHorizon)
		if err != nil || horizon <= 0 || horizon > maxHorizon {
			horizon = defaultHorizon
		}

		// Fixtures and prices move on the scale of hours, not the sixty
		// seconds a live gameweek's scores do, so this gets a much longer
		// shared cache than /api/report.
		//
		// max-age=0 is doing real work here: with only s-maxage set, the
		// response is cacheable-but-unversioned to a browser, and Chrome
		// will hold it under heuristic freshness. A visitor then keeps one
		// board for the rest of the session and sees no new fields, no new
		// prices and no new fixtures. The CDN still caches for 15 minutes;
		// the browser revalidates and gets a 304 when nothing has changed.
		w.Header().Set("Cache-Control", "public, max-age=0, s-maxage=900, stale-while-revalidate=3600")

		key := strconv.Itoa(horizon)
		if b, ok := s.boardCached(key); ok {
			writeJSON(w, b)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()

		board, err := s.client.BuildPlayerBoard(ctx, horizon)
		if err != nil {
			status := http.StatusBadGateway
			msg := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				status = http.StatusGatewayTimeout
				msg = "the FPL API took too long — retry"
			}
			writeErr(w, status, msg)
			return
		}

		s.storeBoard(key, board)
		writeJSON(w, board)
	}
}

func (s *server) boardCached(key string) (*fpl.PlayerBoard, bool) {
	s.boardMu.Lock()
	defer s.boardMu.Unlock()
	e, ok := s.boardCache[key]
	if !ok || time.Since(e.stored) > boardTTL {
		return nil, false
	}
	return e.board, true
}

func (s *server) storeBoard(key string, b *fpl.PlayerBoard) {
	s.boardMu.Lock()
	defer s.boardMu.Unlock()
	s.boardCache[key] = boardCacheEntry{board: b, stored: time.Now()}
}

func (s *server) teamCached(key string) (*fpl.TeamDetail, bool) {
	s.teamMu.Lock()
	defer s.teamMu.Unlock()
	e, ok := s.teamCache[key]
	if !ok || time.Since(e.stored) > cacheTTL {
		return nil, false
	}
	return e.detail, true
}

func (s *server) storeTeam(key string, det *fpl.TeamDetail) {
	s.teamMu.Lock()
	defer s.teamMu.Unlock()
	s.teamCache[key] = teamCacheEntry{detail: det, stored: time.Now()}
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
