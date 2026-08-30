package fpl

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// ChipLabel maps the API's chip strings to the short forms used in the UI.
func ChipLabel(name string) string {
	switch name {
	case "wildcard":
		return "WC"
	case "freehit":
		return "FH"
	case "bboost":
		return "BB"
	case "3xc":
		return "TC"
	case "":
		return ""
	default:
		return name
	}
}

// ManagerGW is one manager's line for a single gameweek.
type ManagerGW struct {
	Entry      string `json:"entry"`
	EntryID    int    `json:"entryId"`
	Manager    string `json:"manager"`
	LeagueRank int    `json:"leagueRank"`
	LastRank   int    `json:"lastRank"`
	Total      int    `json:"total"` // net, cumulative

	// Played is false when the manager had not joined by this gameweek.
	Played bool `json:"played"`

	GrossPoints int    `json:"grossPoints"`
	Hit         int    `json:"hit"`
	NetPoints   int    `json:"netPoints"`
	GWRank      int    `json:"gwRank"`
	OverallRank int    `json:"overallRank"`
	Chip        string `json:"chip"`

	// Bench is points_on_bench, except during a Bench Boost where the API
	// reports 0 and we recompute the real figure from picks + live data.
	Bench          int           `json:"bench"`
	BenchDerived   bool          `json:"benchDerived"`
	Transfers      int           `json:"transfers"`
	TransferDetail []TransferOut `json:"transferDetail,omitempty"`

	// Captain is who was picked, and the ToPlay/PlayingNow/FinishedPlaying
	// counts are over the starting XI - or all 15 during a Bench Boost, the
	// same set of players whose fixtures still count towards the score.
	Captain         string `json:"captain"`
	ToPlay          int    `json:"toPlay"`
	PlayingNow      int    `json:"playingNow"`
	FinishedPlaying int    `json:"finishedPlaying"`
	SquadCount      int    `json:"squadCount"`

	// WonGW / WonBench mark the winners of this particular gameweek. Ties are
	// shared, so more than one manager can carry either badge.
	WonGW    bool `json:"wonGw"`
	WonBench bool `json:"wonBench"`
}

// TransferOut is the outward shape of a transfer. The Transfer type it comes
// from carries snake_case tags because those decode the FPL API's own field
// names; re-serialising it put element_out into a payload that is camelCase
// everywhere else, which the frontend then read as undefined.
type TransferOut struct {
	In      int `json:"in"`
	Out     int `json:"out"`
	InCost  int `json:"inCost"`
	OutCost int `json:"outCost"`
	Event   int `json:"event"`
}

func toTransferOut(t Transfer) TransferOut {
	return TransferOut{
		In:      t.ElementIn,
		Out:     t.ElementOut,
		InCost:  t.ElementInCost,
		OutCost: t.ElementOutCost,
		Event:   t.Event,
	}
}

// ManagerSeason aggregates a manager's whole season.
type ManagerSeason struct {
	Entry      string `json:"entry"`
	EntryID    int    `json:"entryId"`
	Manager    string `json:"manager"`
	LeagueRank int    `json:"leagueRank"`
	Total      int    `json:"total"`

	BenchTotal     int        `json:"benchTotal"`
	HitsTotal      int        `json:"hitsTotal"`
	TransfersTotal int        `json:"transfersTotal"`
	BestGW         int        `json:"bestGw"`
	BestGWPoints   int        `json:"bestGwPoints"`
	ChipsUsed      []ChipPlay `json:"chipsUsed"`

	// Weekly honours, tallied across every gameweek played so far.
	GWWins        int      `json:"gwWins"`
	GWWinWeeks    []int    `json:"gwWinWeeks"`
	BenchWins     int      `json:"benchWins"`
	BenchWinWeeks []int    `json:"benchWinWeeks"`
	MonthWins     int      `json:"monthWins"`
	MonthWinNames []string `json:"monthWinNames"`
}

// Report is everything the CLI renders.
type Report struct {
	League      League `json:"league"`
	Event       Event  `json:"event"`
	Provisional bool   `json:"provisional"` // not data_checked: ranks/bench can still move
	NewEntries  int    `json:"newEntries"`
	// Events lists the gameweeks that have already started, so the UI can
	// offer a gameweek picker without its own bootstrap call.
	Events      []Event         `json:"events"`
	Gameweek    []ManagerGW     `json:"gameweek"`
	Season      []ManagerSeason `json:"season"`
	Monthly     []MonthlyPhase  `json:"monthly"`
	PlayerNames map[int]string  `json:"playerNames"`
}

// MonthlyPhase is one calendar-month leaderboard (an FPL "phase" other than
// the season-long Overall one), scored on points already fetched via
// entry/{id}/history/ - no extra API calls.
type MonthlyPhase struct {
	Name       string `json:"name"`
	StartEvent int    `json:"startEvent"`
	StopEvent  int    `json:"stopEvent"`
	// Complete is true once every gameweek in the phase has been played, so
	// the standings shown cannot still change.
	Complete  bool         `json:"complete"`
	Standings []MonthlyRow `json:"standings"`
}

type MonthlyRow struct {
	EntryID int    `json:"entryId"`
	Manager string `json:"manager"`
	Entry   string `json:"entry"`
	Points  int    `json:"points"`
	Won     bool   `json:"won"`
}

// Options controls how much the report fetches.
type Options struct {
	GW          int  // 0 = current gameweek
	MaxManagers int  // 0 = no limit
	Concurrency int  // simultaneous in-flight requests
	WithDetail  bool // fetch entry/{id}/transfers/ for in/out player names
}

type entryData struct {
	row       StandingRow
	history   *History
	transfers []Transfer
	// picks is nil when the gameweek 404s (not played yet, or joined the
	// league after it) - that is not an error worth failing the report over,
	// it just leaves Captain/ToPlay blank for that manager.
	picks *PicksResponse
	err   error
}

// BuildReport follows the fetch order in FPL_API_NOTES.md: bootstrap, then
// standings, then one history call per manager (which alone covers bench
// points, gameweek rank, chips and hits for every gameweek), and only then the
// optional per-manager transfer detail.
func (c *Client) BuildReport(ctx context.Context, leagueID int, opts Options) (*Report, error) {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 6
	}

	boot, err := c.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}

	ev, ok := boot.CurrentEvent()
	if opts.GW > 0 {
		ev, ok = boot.Event(opts.GW)
	}
	if !ok {
		return nil, errNoGameweek{opts.GW}
	}

	league, rows, newEnts, err := c.ClassicLeague(ctx, leagueID, opts.MaxManagers)
	if err != nil {
		return nil, err
	}

	data := make([]entryData, len(rows))
	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup

	for i, row := range rows {
		wg.Add(1)
		go func(i int, row StandingRow) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			d := entryData{row: row}
			d.history, d.err = c.EntryHistory(ctx, row.Entry)
			if d.err == nil && opts.WithDetail {
				d.transfers, d.err = c.EntryTransfers(ctx, row.Entry)
			}
			if d.err == nil {
				picks, perr := c.EntryPicks(ctx, row.Entry, ev.ID)
				if perr != nil && !NotFound(perr) {
					d.err = perr
				} else {
					d.picks = picks
				}
			}
			data[i] = d
		}(i, row)
	}
	wg.Wait()

	for _, d := range data {
		if d.err != nil {
			return nil, d.err
		}
	}

	rep := &Report{
		League:      league,
		Event:       ev,
		Provisional: !ev.DataChecked,
		NewEntries:  len(newEnts.Results),
		PlayerNames: map[int]string{},
	}
	for _, e := range boot.Events {
		if e.IsCurrent || e.IsPrevious || e.Finished || e.ID < ev.ID {
			rep.Events = append(rep.Events, e)
		}
	}
	if len(rep.Events) == 0 {
		rep.Events = append(rep.Events, ev)
	}

	elements := make(map[int]Element, len(boot.Elements))
	for _, e := range boot.Elements {
		elements[e.ID] = e
	}

	// Fixtures back the Captain/To-play columns: every pick's club is looked
	// up against this gameweek's matches to say whether it has yet to kick
	// off, is in progress, or is done.
	fixtures, err := c.Fixtures(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	statusByTeam := teamFixtureStatus(fixtures)

	// Bench points come from entry/history/'s points_on_bench, except:
	//   - a Bench Boost week always reports 0 there, however long ago it was
	//     played, so it must be recomputed from picks + live points; and
	//   - while the gameweek itself is still live (not data_checked), that
	//     cached history figure lags behind actual scoring the same way
	//     grossPoints does above, so it needs the same live recompute.
	// Both draw from one live points map. Every manager's picks are already
	// in hand from the fetch loop above (Captain/To-play need them too), so
	// this is pure computation now rather than a second round of fetches.
	live := ev.IsCurrent && !ev.DataChecked

	needsLive := live
	if !needsLive {
		for _, d := range data {
			if d.picks != nil && d.picks.ActiveChip == "bboost" {
				needsLive = true
				break
			}
		}
	}

	var livePoints map[int]int
	if needsLive {
		liveResp, err := c.EventLive(ctx, ev.ID)
		if err != nil {
			return nil, err
		}
		livePoints = liveResp.PointsByElement()
	}

	benchOverride := make([]int, len(data))
	benchIsBB := make([]bool, len(data))
	for i := range benchOverride {
		benchOverride[i] = -1
	}
	if livePoints != nil {
		for i, d := range data {
			if d.picks == nil {
				continue
			}
			isBB := d.picks.ActiveChip == "bboost"
			if !isBB && !live {
				continue
			}
			total := 0
			for _, p := range d.picks.Picks {
				if p.Position > 11 {
					total += livePoints[p.Element]
				}
			}
			benchOverride[i] = total
			benchIsBB[i] = isBB
		}
	}

	for i, d := range data {
		gw := ManagerGW{
			EntryID:    d.row.Entry,
			Entry:      d.row.EntryName,
			Manager:    d.row.PlayerName,
			LeagueRank: d.row.Rank,
			LastRank:   d.row.LastRank,
			Total:      d.row.Total,
		}

		if h, ok := gwRow(d.history.Current, ev.ID); ok {
			gw.Played = true
			gw.GrossPoints = h.Points
			gw.Hit = h.EventTransfersCost
			gw.NetPoints = h.Net()
			gw.GWRank = h.Rank
			gw.OverallRank = h.OverallRank
			gw.Bench = h.PointsOnBench
			gw.Transfers = h.EventTransfers

			// entry/history/ is cached by FPL and lags well behind live play -
			// leagues-classic/standings/ recomputes event_total from live data
			// on every request, so prefer it while the gameweek is still live.
			if live {
				gw.GrossPoints = d.row.EventTotal
				gw.NetPoints = d.row.EventTotal - gw.Hit
			}
		}

		chip := chipFor(d.history.Chips, ev.ID)
		gw.Chip = ChipLabel(chip)

		if gw.Played && benchOverride[i] >= 0 {
			gw.Bench = benchOverride[i]
			gw.BenchDerived = benchIsBB[i]
		}

		if d.picks != nil {
			isBB := d.picks.ActiveChip == "bboost"
			for _, p := range d.picks.Picks {
				if p.IsCaptain {
					gw.Captain = elements[p.Element].WebName
				}
				if p.Position > 11 && !isBB {
					continue // bench doesn't count towards the score, unless boosted
				}
				gw.SquadCount++
				switch statusByTeam[elements[p.Element].Team] {
				case "live":
					gw.PlayingNow++
				case "upcoming":
					gw.ToPlay++
				default: // "finished", "none", or a club with no fixture data
					gw.FinishedPlaying++
				}
			}
		}

		for _, t := range d.transfers {
			if t.Event == ev.ID {
				gw.TransferDetail = append(gw.TransferDetail, toTransferOut(t))
			}
		}

		rep.Gameweek = append(rep.Gameweek, gw)
		rep.Season = append(rep.Season, seasonFor(d))
	}

	awardWeeklyWins(data, rep)
	rep.Monthly = monthlyPhases(data, boot.Phases, ev)
	awardMonthlyWins(rep)

	// Ship names only for the players that actually appear in this response.
	// The full map is all 622 players and was half the payload, to render a
	// handful of transfer rows.
	allNames := boot.PlayerNames()
	for _, g := range rep.Gameweek {
		for _, t := range g.TransferDetail {
			rep.PlayerNames[t.In] = allNames[t.In]
			rep.PlayerNames[t.Out] = allNames[t.Out]
		}
	}

	sort.Slice(rep.Gameweek, func(i, j int) bool {
		return rep.Gameweek[i].LeagueRank < rep.Gameweek[j].LeagueRank
	})
	sort.Slice(rep.Season, func(i, j int) bool {
		return rep.Season[i].LeagueRank < rep.Season[j].LeagueRank
	})
	return rep, nil
}

// monthlyPhases scores every non-Overall phase using the per-gameweek net
// points already present in each manager's history. The season-long phase is
// excluded by name, since the API does not guarantee its id stays 1.
func monthlyPhases(data []entryData, phases []Phase, current Event) []MonthlyPhase {
	var out []MonthlyPhase
	for _, ph := range phases {
		if strings.EqualFold(ph.Name, "Overall") {
			continue
		}
		if ph.StartEvent > current.ID {
			continue // has not started
		}

		best := 0
		rows := make([]MonthlyRow, 0, len(data))
		for _, d := range data {
			total, played := 0, false
			for _, h := range d.history.Current {
				if h.Event >= ph.StartEvent && h.Event <= ph.StopEvent {
					total += h.Net()
					played = true
				}
			}
			if !played {
				continue
			}
			rows = append(rows, MonthlyRow{
				EntryID: d.row.Entry,
				Manager: d.row.PlayerName,
				Entry:   d.row.EntryName,
				Points:  total,
			})
			if total > best {
				best = total
			}
		}
		if len(rows) == 0 {
			continue
		}
		for i := range rows {
			rows[i].Won = best > 0 && rows[i].Points == best
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Points > rows[j].Points })

		out = append(out, MonthlyPhase{
			Name:       ph.Name,
			StartEvent: ph.StartEvent,
			StopEvent:  ph.StopEvent,
			Complete:   ph.StopEvent < current.ID || (ph.StopEvent == current.ID && current.DataChecked),
			Standings:  rows,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartEvent < out[j].StartEvent })
	return out
}

// awardMonthlyWins folds the monthly standings back onto each manager's season
// row, the same way the weekly honours are tallied.
func awardMonthlyWins(rep *Report) {
	wins := map[int][]string{}
	for _, ph := range rep.Monthly {
		for _, row := range ph.Standings {
			if row.Won {
				wins[row.EntryID] = append(wins[row.EntryID], ph.Name)
			}
		}
	}
	for i := range rep.Season {
		names := wins[rep.Season[i].EntryID]
		rep.Season[i].MonthWins = len(names)
		rep.Season[i].MonthWinNames = names
	}
}

// awardWeeklyWins tallies the two weekly honours across every gameweek any
// manager in the league has played. It reads only the histories already
// fetched, so it costs no extra API calls.
//
// Gameweek winner is the highest score NET of the transfer hit — the figure the
// league table moves by, and the one a manager would argue about.
//
// Bench winner is the most points left behind, which is points_on_bench as the
// API reports it. That deliberately means a Bench Boost week cannot win it: the
// bench played, so nothing was left behind. The displayed bench figure for such
// a week is the recomputed haul (marked with *), which answers a different
// question and is not what the tally counts.
func awardWeeklyWins(data []entryData, rep *Report) {
	// Collect every gameweek that appears in anyone's history.
	weeks := map[int]bool{}
	for _, d := range data {
		for _, h := range d.history.Current {
			weeks[h.Event] = true
		}
	}

	type honours struct{ gw, bench []int }
	byEntry := map[int]*honours{}
	for _, d := range data {
		byEntry[d.row.Entry] = &honours{}
	}

	for week := range weeks {
		bestPts, bestBench := 0, 0
		for _, d := range data {
			h, ok := gwRow(d.history.Current, week)
			if !ok {
				continue
			}
			if h.Net() > bestPts {
				bestPts = h.Net()
			}
			if h.PointsOnBench > bestBench {
				bestBench = h.PointsOnBench
			}
		}
		for _, d := range data {
			h, ok := gwRow(d.history.Current, week)
			if !ok {
				continue
			}
			// A zero best means nobody scored / nobody benched anything, which
			// is not a win worth recording.
			if bestPts > 0 && h.Net() == bestPts {
				byEntry[d.row.Entry].gw = append(byEntry[d.row.Entry].gw, week)
			}
			if bestBench > 0 && h.PointsOnBench == bestBench {
				byEntry[d.row.Entry].bench = append(byEntry[d.row.Entry].bench, week)
			}
		}
	}

	for i := range rep.Season {
		h := byEntry[rep.Season[i].EntryID]
		if h == nil {
			continue
		}
		sort.Ints(h.gw)
		sort.Ints(h.bench)
		rep.Season[i].GWWins, rep.Season[i].GWWinWeeks = len(h.gw), h.gw
		rep.Season[i].BenchWins, rep.Season[i].BenchWinWeeks = len(h.bench), h.bench
	}

	for i := range rep.Gameweek {
		h := byEntry[rep.Gameweek[i].EntryID]
		if h == nil {
			continue
		}
		rep.Gameweek[i].WonGW = contains(h.gw, rep.Event.ID)
		rep.Gameweek[i].WonBench = contains(h.bench, rep.Event.ID)
	}
}

func contains(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func seasonFor(d entryData) ManagerSeason {
	s := ManagerSeason{
		EntryID:    d.row.Entry,
		Entry:      d.row.EntryName,
		Manager:    d.row.PlayerName,
		LeagueRank: d.row.Rank,
		Total:      d.row.Total,
		ChipsUsed:  d.history.Chips,
	}
	for _, h := range d.history.Current {
		s.BenchTotal += h.PointsOnBench
		s.HitsTotal += h.EventTransfersCost
		s.TransfersTotal += h.EventTransfers
		if h.Net() > s.BestGWPoints {
			s.BestGWPoints, s.BestGW = h.Net(), h.Event
		}
	}
	return s
}

func gwRow(rows []GWHistory, gw int) (GWHistory, bool) {
	for _, h := range rows {
		if h.Event == gw {
			return h, true
		}
	}
	return GWHistory{}, false
}

func chipFor(chips []ChipPlay, gw int) string {
	for _, c := range chips {
		if c.Event == gw {
			return c.Name
		}
	}
	return ""
}

type errNoGameweek struct{ gw int }

func (e errNoGameweek) Error() string {
	if e.gw > 0 {
		return "no such gameweek"
	}
	return "could not determine the current gameweek"
}
