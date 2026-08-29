package fpl

import (
	"context"
	"sort"
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
	Bench          int        `json:"bench"`
	BenchDerived   bool       `json:"benchDerived"`
	Transfers      int        `json:"transfers"`
	TransferDetail []Transfer `json:"transferDetail,omitempty"`
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
	PlayerNames map[int]string  `json:"playerNames"`
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
	err       error
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
		PlayerNames: boot.PlayerNames(),
	}
	for _, e := range boot.Events {
		if e.IsCurrent || e.IsPrevious || e.Finished || e.ID < ev.ID {
			rep.Events = append(rep.Events, e)
		}
	}
	if len(rep.Events) == 0 {
		rep.Events = append(rep.Events, ev)
	}

	// Managers on a Bench Boost report points_on_bench as 0. Recover the real
	// figure from picks + live, but only fetch live if someone actually used it.
	var livePoints map[int]int
	needsLive := false
	for _, d := range data {
		if chipFor(d.history.Chips, ev.ID) == "bboost" {
			needsLive = true
			break
		}
	}
	if needsLive {
		live, err := c.EventLive(ctx, ev.ID)
		if err != nil {
			return nil, err
		}
		livePoints = live.PointsByElement()
	}

	for _, d := range data {
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
		}

		chip := chipFor(d.history.Chips, ev.ID)
		gw.Chip = ChipLabel(chip)

		if chip == "bboost" && livePoints != nil && gw.Played {
			if b, ok := c.trueBench(ctx, d.row.Entry, ev.ID, livePoints); ok {
				gw.Bench = b
				gw.BenchDerived = true
			}
		}

		for _, t := range d.transfers {
			if t.Event == ev.ID {
				gw.TransferDetail = append(gw.TransferDetail, t)
			}
		}

		rep.Gameweek = append(rep.Gameweek, gw)
		rep.Season = append(rep.Season, seasonFor(d))
	}

	sort.Slice(rep.Gameweek, func(i, j int) bool {
		return rep.Gameweek[i].LeagueRank < rep.Gameweek[j].LeagueRank
	})
	sort.Slice(rep.Season, func(i, j int) bool {
		return rep.Season[i].LeagueRank < rep.Season[j].LeagueRank
	})
	return rep, nil
}

// trueBench sums live points for picks outside the XI. During a Bench Boost
// every pick has multiplier 1, so position is the only reliable discriminator.
func (c *Client) trueBench(ctx context.Context, entryID, gw int, live map[int]int) (int, bool) {
	picks, err := c.EntryPicks(ctx, entryID, gw)
	if err != nil {
		return 0, false
	}
	total := 0
	for _, p := range picks.Picks {
		if p.Position > 11 {
			total += live[p.Element]
		}
	}
	return total, true
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
