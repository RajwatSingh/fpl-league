package fpl

import "time"

// ---- bootstrap-static/ ----

type Bootstrap struct {
	Events   []Event   `json:"events"`
	Elements []Element `json:"elements"`
	Teams    []Team    `json:"teams"`
	Chips    []ChipDef `json:"chips"`
	Phases   []Phase   `json:"phases"`
}

// Phase is one monthly (or overall) leaderboard window. The season-long phase
// is always named "Overall" - callers wanting just the calendar months should
// filter it out by name rather than assume its id, which is not guaranteed.
type Phase struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	StartEvent int    `json:"start_event"`
	StopEvent  int    `json:"stop_event"`
}

type Event struct {
	ID                int       `json:"id"`
	Name              string    `json:"name"`
	DeadlineTime      time.Time `json:"deadline_time"`
	AverageEntryScore int       `json:"average_entry_score"`
	Finished          bool      `json:"finished"`
	DataChecked       bool      `json:"data_checked"`
	IsPrevious        bool      `json:"is_previous"`
	IsCurrent         bool      `json:"is_current"`
	IsNext            bool      `json:"is_next"`
}

type Element struct {
	ID          int    `json:"id"`
	WebName     string `json:"web_name"`
	ElementType int    `json:"element_type"`
	Team        int    `json:"team"`
	NowCost     int    `json:"now_cost"`

	// Removed marks an element the game has withdrawn - a player who left
	// the league mid-season. They keep their id so past picks still
	// resolve, but they have no fixtures ahead of them.
	Removed bool `json:"removed"`

	// Status is availability: a, d (doubtful), i (injured), s (suspended),
	// u (unavailable). ChanceOfPlayingNextRound is null unless the club has
	// given a percentage, hence the pointer - 0 and "no figure given" are
	// different answers and must not collapse into each other.
	Status                   string `json:"status"`
	News                     string `json:"news"`
	ChanceOfPlayingNextRound *int   `json:"chance_of_playing_next_round"`

	// Season totals.
	TotalPoints           int `json:"total_points"`
	Minutes               int `json:"minutes"`
	Starts                int `json:"starts"`
	GoalsScored           int `json:"goals_scored"`
	Assists               int `json:"assists"`
	CleanSheets           int `json:"clean_sheets"`
	Bonus                 int `json:"bonus"`
	DefensiveContribution int `json:"defensive_contribution"`
	// The rate as well as the count. The ticker column shows the season
	// total, where it sits beside Points and Form and reads as one of
	// them; the player card has room for both, and there the rate is what
	// compares a starter against someone who has played four hours.
	DefensiveContributionPer90 float64 `json:"defensive_contribution_per_90"`

	// The API sends every rate and expectation as a string, so these are
	// parsed at the edge rather than fought with a custom unmarshaller.
	Form              string `json:"form"`
	PointsPerGame     string `json:"points_per_game"`
	SelectedByPercent string `json:"selected_by_percent"`
	EPNext            string `json:"ep_next"`
	ExpectedGoals     string `json:"expected_goals"`
	ExpectedAssists   string `json:"expected_assists"`
	// Per-90 concession: the club's own defensive rate, when read off a
	// keeper who played the full match. These two arrive as numbers, unlike
	// every other rate on this struct.
	GoalsConcededPer90         float64 `json:"goals_conceded_per_90"`
	ExpectedGoalsConcededPer90 float64 `json:"expected_goals_conceded_per_90"`
}

type Team struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
}

// ChipDef describes one chip instance. Since the two-half rework each chip is
// issued twice, so name alone is not unique - start_event/stop_event are.
type ChipDef struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	Number     int    `json:"number"`
	StartEvent int    `json:"start_event"`
	StopEvent  int    `json:"stop_event"`
	ChipType   string `json:"chip_type"`
}

// CurrentEvent returns the gameweek marked is_current, falling back to is_next
// (pre-season, before GW1 kicks off).
func (b *Bootstrap) CurrentEvent() (Event, bool) {
	for _, e := range b.Events {
		if e.IsCurrent {
			return e, true
		}
	}
	for _, e := range b.Events {
		if e.IsNext {
			return e, true
		}
	}
	return Event{}, false
}

func (b *Bootstrap) Event(id int) (Event, bool) {
	for _, e := range b.Events {
		if e.ID == id {
			return e, true
		}
	}
	return Event{}, false
}

// PlayerNames maps element id -> web name, for rendering transfers.
func (b *Bootstrap) PlayerNames() map[int]string {
	m := make(map[int]string, len(b.Elements))
	for _, e := range b.Elements {
		m[e.ID] = e.WebName
	}
	return m
}

// ---- leagues-classic/{id}/standings/ ----

type LeagueResponse struct {
	League     League     `json:"league"`
	Standings  Standings  `json:"standings"`
	NewEntries NewEntries `json:"new_entries"`
}

type League struct {
	ID         int    `json:"id"`
	Name       string `json:"name"`
	LeagueType string `json:"league_type"`
	Scoring    string `json:"scoring"`
	StartEvent int    `json:"start_event"`
}

type Standings struct {
	HasNext bool          `json:"has_next"`
	Page    int           `json:"page"`
	Results []StandingRow `json:"results"`
}

type StandingRow struct {
	Entry      int    `json:"entry"`
	EntryName  string `json:"entry_name"`
	PlayerName string `json:"player_name"`
	Rank       int    `json:"rank"`
	LastRank   int    `json:"last_rank"`
	RankSort   int    `json:"rank_sort"`
	// EventTotal is GROSS - it does not subtract the transfer hit.
	EventTotal int `json:"event_total"`
	// Total is NET - hits are already deducted.
	Total int `json:"total"`
}

// NewEntries holds managers who joined mid-season and are not yet ranked.
type NewEntries struct {
	HasNext bool          `json:"has_next"`
	Results []NewEntryRow `json:"results"`
}

type NewEntryRow struct {
	Entry           int    `json:"entry"`
	EntryName       string `json:"entry_name"`
	PlayerFirstName string `json:"player_first_name"`
	PlayerLastName  string `json:"player_last_name"`
}

// ---- entry/{id}/history/ ----

type History struct {
	Current []GWHistory  `json:"current"`
	Past    []PastSeason `json:"past"`
	Chips   []ChipPlay   `json:"chips"`
}

// GWHistory is one manager's row for one gameweek. Also returned inline as
// entry_history on the picks endpoint.
type GWHistory struct {
	Event int `json:"event"`
	// Points is GROSS: the transfer hit is NOT deducted. Use Net().
	Points int `json:"points"`
	// TotalPoints is NET and cumulative.
	TotalPoints        int `json:"total_points"`
	Rank               int `json:"rank"`         // rank within this gameweek
	OverallRank        int `json:"overall_rank"` // cumulative season rank
	PercentileRank     int `json:"percentile_rank"`
	Bank               int `json:"bank"`
	Value              int `json:"value"`
	EventTransfers     int `json:"event_transfers"`
	EventTransfersCost int `json:"event_transfers_cost"`
	// PointsOnBench is 0 during a Bench Boost - see TrueBench.
	PointsOnBench int `json:"points_on_bench"`
}

// Net is the gameweek score after the transfer hit, which is what the FPL UI
// shows and what the season total actually moves by.
func (g GWHistory) Net() int { return g.Points - g.EventTransfersCost }

type PastSeason struct {
	SeasonName  string `json:"season_name"`
	TotalPoints int    `json:"total_points"`
	Rank        int    `json:"rank"`
}

type ChipPlay struct {
	Name  string    `json:"name"`
	Event int       `json:"event"`
	Time  time.Time `json:"time"`
}

// ---- entry/{id}/transfers/ ----

type Transfer struct {
	ElementIn      int       `json:"element_in"`
	ElementInCost  int       `json:"element_in_cost"`
	ElementOut     int       `json:"element_out"`
	ElementOutCost int       `json:"element_out_cost"`
	Entry          int       `json:"entry"`
	Event          int       `json:"event"`
	Time           time.Time `json:"time"`
}

// ---- entry/{id}/event/{gw}/picks/ ----

type PicksResponse struct {
	ActiveChip    string    `json:"active_chip"`
	AutomaticSubs []AutoSub `json:"automatic_subs"`
	EntryHistory  GWHistory `json:"entry_history"`
	Picks         []Pick    `json:"picks"`
}

// Pick positions 1-11 are the XI and 12-15 the bench. Auto-subs are already
// applied to these positions, so do not re-apply AutomaticSubs.
type Pick struct {
	Element       int  `json:"element"`
	Position      int  `json:"position"`
	Multiplier    int  `json:"multiplier"`
	IsCaptain     bool `json:"is_captain"`
	IsViceCaptain bool `json:"is_vice_captain"`
	ElementType   int  `json:"element_type"`
}

type AutoSub struct {
	Entry      int `json:"entry"`
	ElementIn  int `json:"element_in"`
	ElementOut int `json:"element_out"`
	Event      int `json:"event"`
}

// ---- event/{gw}/live/ ----

type LiveResponse struct {
	Elements []LiveElement `json:"elements"`
}

type LiveElement struct {
	ID    int       `json:"id"`
	Stats LiveStats `json:"stats"`
}

type LiveStats struct {
	Minutes     int  `json:"minutes"`
	TotalPoints int  `json:"total_points"`
	Played      bool `json:"played"`
}

// PointsByElement flattens a live response into element id -> points.
func (l *LiveResponse) PointsByElement() map[int]int {
	m := make(map[int]int, len(l.Elements))
	for _, e := range l.Elements {
		m[e.ID] = e.Stats.TotalPoints
	}
	return m
}

// ---- fixtures/?event={gw} ----

type Fixture struct {
	ID       int  `json:"id"`
	Event    int  `json:"event"`
	TeamH    int  `json:"team_h"`
	TeamA    int  `json:"team_a"`
	Started  bool `json:"started"`
	Finished bool `json:"finished"`

	// KickoffTime is what makes a midweek round and a short turnaround
	// visible; it is null only for a fixture with no date yet.
	KickoffTime time.Time `json:"kickoff_time"`
	// Scores are null until the match is played, so they are pointers -
	// a 0-0 and an unplayed match must not read the same.
	TeamHScore *int `json:"team_h_score"`
	TeamAScore *int `json:"team_a_score"`
	// The game's own 1-5 difficulty, kept for comparison against ours.
	TeamHDifficulty int `json:"team_h_difficulty"`
	TeamADifficulty int `json:"team_a_difficulty"`
}
