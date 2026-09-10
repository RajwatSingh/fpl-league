package fpl

import (
	"context"
	"math"
	"sort"
	"strconv"
	"time"
)

// ── the difficulty model ──────────────────────────────────────────────
//
// Every number below is a delta in difficulty points away from a neutral
// fixture, and they are summed onto Base. Keeping them signed and separate
// is the whole point: the UI shows the breakdown, so a 8.4 can be read as
// "away at a top squad" rather than taken on faith. A single opaque weight
// vector would score the same fixtures and explain none of them.
//
// The scale runs 1 (easiest) to 10 (hardest), because "difficulty out of
// 10" reads that way everywhere else in football.
const (
	fdBase = 5.5
	fdMin  = 1.0
	fdMax  = 10.0

	// Opponent squad rating: the largest single term. It is also the only
	// one that barely moves during a season, which is what you want early
	// on, when three matches of xG are mostly noise.
	wSquad = 1.6

	// The matchup term - opponent defence for attackers, opponent attack
	// for defenders. Deliberately just under the squad term: it is the
	// input that actually reflects current form, but it is also the one
	// resting on the fewest matches, and at equal weight a club three
	// games into a hot streak scored harder than the champions.
	wMatchup = 1.0

	// z-scores are capped here, which is what holds wMatchup's range to
	// ±1.5 against the squad term's ±1.6. Without a cap one outlier club
	// sets the scale for the other nineteen.
	zCap = 1.5

	// Home advantage is worth roughly a third of a goal in the Premier
	// League, and this is the pair of deltas that comes to.
	wVenue = 0.7

	// Fatigue caps below the venue term deliberately. A midweek round is a
	// real effect but a smaller one than where the match is played, and
	// letting congestion outweigh venue made Tuesday fixtures against
	// promoted sides score harder than Saturday trips to City.
	wMidweek     = 0.35
	wShortRst    = 0.45
	wVeryShort   = 0.7
	fdFatigueCap = 0.9

	// Injuries move the least. Availability is the noisiest input here -
	// the API's status flags lag real team news by days - so it adjusts a
	// fixture rather than deciding it.
	wInjury  = 3.0
	fdInjCap = 0.8

	// Shrinkage prior, in matches. Rate stats are pulled toward the league
	// mean as though every club had already played this many average
	// matches, so a side that has faced two weak opponents does not read as
	// the best defence in the division in September.
	shrinkPrior = 6.0

	// A rest gap at or under this many days counts as congestion.
	shortRestDays = 4
	veryShortRest = 3
)

// PositionGroup splits the squad the only way that changes what a fixture
// means. A forward wants an opponent who concedes chances; a defender wants
// one who creates none. Scoring both from "how good is the opponent" would
// hand a keeper the same number as the striker in front of him.
const (
	GroupAttack  = "att" // MID, FWD
	GroupDefence = "def" // GKP, DEF
)

func groupFor(elementType int) string {
	if elementType <= 2 {
		return GroupDefence
	}
	return GroupAttack
}

// ── wire types ────────────────────────────────────────────────────────

// FixtureScore is one upcoming match, rated from one club's point of view.
// Both scores travel together because the fixture is shared: only the lens
// differs, and sending it twice would double the payload for nothing.
type FixtureScore struct {
	Event    int       `json:"event"`
	Opponent string    `json:"opponent"` // opponent short name
	OppID    int       `json:"oppId"`
	Home     bool      `json:"home"`
	Kickoff  time.Time `json:"kickoff"`
	Midweek  bool      `json:"midweek"`
	// RestDays is days since this club's previous fixture, or 0 when there
	// is no previous fixture to measure from.
	RestDays int `json:"restDays"`

	// AttScore rates the match for a MID or FWD, DefScore for a GKP or DEF.
	AttScore float64 `json:"attScore"`
	DefScore float64 `json:"defScore"`

	// Terms is the signed breakdown behind the two scores, so the UI can
	// say why. Shared terms appear once; the matchup term differs per
	// group and is carried as AttMatchup/DefMatchup.
	Squad      float64 `json:"squad"`
	Venue      float64 `json:"venue"`
	Fatigue    float64 `json:"fatigue"`
	Injury     float64 `json:"injury"`
	AttMatchup float64 `json:"attMatchup"`
	DefMatchup float64 `json:"defMatchup"`
}

// TeamStrength is one club's rating plus its rated fixture run.
type TeamStrength struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	ShortName string `json:"short"`

	// SquadValue is the combined price of the fifteen most expensive
	// players at the club, in millions - a squad rating that does not need
	// a single match to have been played.
	SquadValue float64 `json:"squadValue"`
	// SquadRating is SquadValue placed on a 0-1 scale across the league.
	SquadRating float64 `json:"squadRating"`

	Played int `json:"played"`
	// XGCPer90 and GCPer90 are the club's defensive record: chances given
	// and goals actually conceded. Both are shrunk toward the league mean.
	XGCPer90 float64 `json:"xgcPer90"`
	GCPer90  float64 `json:"gcPer90"`
	// XGPer90 and GFPer90 are the same two measures at the other end.
	XGPer90 float64 `json:"xgPer90"`
	GFPer90 float64 `json:"gfPer90"`

	// InjuryBurden is the share of this season's minutes belonging to
	// players who are now unavailable, discounted by their reported chance
	// of playing. 0.12 means an eighth of the side is missing.
	InjuryBurden float64  `json:"injuryBurden"`
	InjuryList   []string `json:"injuryList"`

	Fixtures []FixtureScore `json:"fixtures"`
}

// PlayerRow is one player's season to date. The fixture run lives on the
// club, not here - every Arsenal midfielder faces the same ten matches.
type PlayerRow struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Position string `json:"position"`
	Group    string `json:"group"`
	TeamID   int    `json:"teamId"`
	Team     string `json:"team"`

	Cost     float64 `json:"cost"`
	Points   int     `json:"points"`
	Form     float64 `json:"form"`
	PPG      float64 `json:"ppg"`
	Minutes  int     `json:"minutes"`
	Starts   int     `json:"starts"`
	Goals    int     `json:"goals"`
	Assists  int     `json:"assists"`
	CS       int     `json:"cs"`
	XG       float64 `json:"xg"`
	XA       float64 `json:"xa"`
	DefCon   int     `json:"defCon"`
	Bonus    int     `json:"bonus"`
	Selected float64 `json:"selected"`
	EPNext   float64 `json:"epNext"`

	// Status is the FPL availability flag: a, d (doubtful), i (injured),
	// s (suspended), u (unavailable). Chance is the reported percentage
	// when there is one, else -1.
	Status string `json:"status"`
	Chance int    `json:"chance"`
	News   string `json:"news"`

	// FixScore averages the club's rated run through the player's own lens,
	// and Fixtures counts how many actual matches that average covers -
	// which is not Horizon when the run holds a blank or a double.
	FixScore float64 `json:"fixScore"`
	Fixtures int     `json:"fixtures"`
}

// PlayerBoard is everything the players section needs in one response.
type PlayerBoard struct {
	Event    int            `json:"event"`   // first gameweek in the run
	Horizon  int            `json:"horizon"` // gameweeks covered
	Teams    []TeamStrength `json:"teams"`
	Players  []PlayerRow    `json:"players"`
	Averages ModelAverages  `json:"averages"`
}

// ModelAverages are the league means every z-score above is measured
// against. They are sent so the UI can show a club's numbers next to the
// division's rather than in a vacuum.
type ModelAverages struct {
	XGCPer90 float64 `json:"xgcPer90"`
	GCPer90  float64 `json:"gcPer90"`
	XGPer90  float64 `json:"xgPer90"`
	GFPer90  float64 `json:"gfPer90"`
}

// ── build ─────────────────────────────────────────────────────────────

// BuildPlayerBoard rates every club's next `horizon` gameweeks and returns
// them alongside a season-to-date line for every player. It is three
// upstream calls regardless of how many players come back, because the
// whole model is derived from bootstrap-static plus the full fixture list.
func (c *Client) BuildPlayerBoard(ctx context.Context, horizon int) (*PlayerBoard, error) {
	if horizon <= 0 {
		horizon = 10
	}
	boot, err := c.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	fixtures, err := c.AllFixtures(ctx)
	if err != nil {
		return nil, err
	}
	return buildBoard(boot, fixtures, horizon), nil
}

// buildBoard is the whole model, kept free of the network so it can be
// tested against a fixed bootstrap.
func buildBoard(boot *Bootstrap, fixtures []Fixture, horizon int) *PlayerBoard {
	teams := map[int]*TeamStrength{}
	order := make([]int, 0, len(boot.Teams))
	for _, t := range boot.Teams {
		teams[t.ID] = &TeamStrength{ID: t.ID, Name: t.Name, ShortName: t.ShortName}
		order = append(order, t.ID)
	}

	played := matchesPlayed(fixtures)
	rawAtk, rawGF := attackRates(boot, fixtures, played)
	rawXGC, rawGC := defenceRates(boot, played)
	squadValues := squadValues(boot)
	injuries := injuryBurden(boot)

	// Shrink every rate toward the league mean before it is compared with
	// anything. Three gameweeks in, the unshrunk numbers say Sunderland
	// have the best defence in England.
	avg := ModelAverages{
		XGCPer90: mean(rawXGC),
		GCPer90:  mean(rawGC),
		XGPer90:  mean(rawAtk),
		GFPer90:  mean(rawGF),
	}
	xgc := shrinkAll(rawXGC, avg.XGCPer90, played)
	gc := shrinkAll(rawGC, avg.GCPer90, played)
	xg := shrinkAll(rawAtk, avg.XGPer90, played)
	gf := shrinkAll(rawGF, avg.GFPer90, played)

	// The squad rating is a 0-1 position within the league's own spread,
	// not an absolute: what matters is that this opponent is the third
	// richest squad in the division, not that they cost £95m.
	minV, maxV := span(squadValues)
	for _, id := range order {
		t := teams[id]
		t.Played = played[id]
		t.XGCPer90, t.GCPer90 = round2(xgc[id]), round2(gc[id])
		t.XGPer90, t.GFPer90 = round2(xg[id]), round2(gf[id])
		t.SquadValue = round1(squadValues[id])
		if maxV > minV {
			t.SquadRating = round2((squadValues[id] - minV) / (maxV - minV))
		}
		t.InjuryBurden = round2(injuries[id].burden)
		t.InjuryList = injuries[id].names
	}

	// Defensive solidity for the attacker's lens blends chances given with
	// goals actually conceded. xG is the better predictor, but the goals
	// are what the question asked about and what a manager can check
	// against a league table, so both are in and xG leads.
	conceded := map[int]float64{}
	created := map[int]float64{}
	for _, id := range order {
		conceded[id] = 0.65*xgc[id] + 0.35*gc[id]
		created[id] = 0.65*xg[id] + 0.35*gf[id]
	}
	concededSD := stddev(conceded)
	createdSD := stddev(created)
	concededAvg := mean(conceded)
	createdAvg := mean(created)

	first := nextEvent(boot)
	last := first + horizon - 1
	prevKick := lastKickoff(fixtures, first)

	// Rest days are measured by carrying each club's previous kickoff
	// forward through this loop, so it has to run in kickoff order. The API
	// returns fixtures by id, which is close to chronological and not
	// promised to be - and one pair out of order would hand a club a
	// negative turnaround.
	run := append([]Fixture(nil), fixtures...)
	sort.SliceStable(run, func(i, j int) bool {
		return run[i].KickoffTime.Before(run[j].KickoffTime)
	})

	for _, f := range run {
		if f.Event < first || f.Event > last || f.Finished || f.KickoffTime.IsZero() {
			continue
		}
		for _, side := range []struct {
			club, opp int
			home      bool
		}{{f.TeamH, f.TeamA, true}, {f.TeamA, f.TeamH, false}} {
			t, ok := teams[side.club]
			o := teams[side.opp]
			if !ok || o == nil {
				continue
			}
			fs := FixtureScore{
				Event:    f.Event,
				Opponent: o.ShortName,
				OppID:    o.ID,
				Home:     side.home,
				Kickoff:  f.KickoffTime,
				Midweek:  isMidweek(f.KickoffTime),
			}

			// A stronger opponent is a harder fixture whichever end of the
			// pitch you play at, so this term is shared.
			fs.Squad = wSquad * (2*o.SquadRating - 1)

			// Facing a defence that gives up few chances is hard for an
			// attacker; facing an attack that creates many is hard for a
			// defender. Both are z-scores, so both are already on the same
			// scale as the squad term.
			fs.AttMatchup = wMatchup * z(concededAvg-conceded[o.ID], concededSD)
			fs.DefMatchup = wMatchup * z(created[o.ID]-createdAvg, createdSD)

			if side.home {
				fs.Venue = -wVenue
			} else {
				fs.Venue = wVenue
			}

			// Fatigue is midweek plus turnaround, and the two stack: a
			// Wednesday night three days after a Sunday is the case this
			// term exists for.
			fatigue := 0.0
			if fs.Midweek {
				fatigue += wMidweek
			}
			if prev, ok := prevKick[side.club]; ok {
				days := int(f.KickoffTime.Sub(prev).Hours() / 24)
				fs.RestDays = days
				switch {
				case days <= veryShortRest:
					fatigue += wVeryShort
				case days <= shortRestDays:
					fatigue += wShortRst
				}
			}
			fs.Fatigue = math.Min(fatigue, fdFatigueCap)

			// A depleted opponent is an easier afternoon; being depleted
			// yourself is a harder one. Netting them means a fixture
			// between two equally injury-hit squads is not adjusted at all,
			// which is the right answer.
			fs.Injury = clamp(wInjury*(t.InjuryBurden-o.InjuryBurden), -fdInjCap, fdInjCap)

			shared := fs.Squad + fs.Venue + fs.Fatigue + fs.Injury
			fs.AttScore = round1(clamp(fdBase+shared+fs.AttMatchup, fdMin, fdMax))
			fs.DefScore = round1(clamp(fdBase+shared+fs.DefMatchup, fdMin, fdMax))
			fs.Squad, fs.Venue = round2(fs.Squad), round2(fs.Venue)
			fs.Fatigue, fs.Injury = round2(fs.Fatigue), round2(fs.Injury)
			fs.AttMatchup, fs.DefMatchup = round2(fs.AttMatchup), round2(fs.DefMatchup)

			t.Fixtures = append(t.Fixtures, fs)
			prevKick[side.club] = f.KickoffTime
		}
	}

	board := &PlayerBoard{Event: first, Horizon: horizon, Averages: avg}
	for _, id := range order {
		t := teams[id]
		sort.SliceStable(t.Fixtures, func(i, j int) bool {
			return t.Fixtures[i].Kickoff.Before(t.Fixtures[j].Kickoff)
		})
		board.Teams = append(board.Teams, *t)
	}
	board.Players = playerRows(boot, teams)
	return board
}

// playerRows flattens the element list, dropping players who have left the
// league. A permanent transfer abroad keeps its element with status "u"
// and no club minutes for the rest of the season; showing them would put a
// dozen names with no fixtures at the top of a search for "Watkins".
func playerRows(boot *Bootstrap, teams map[int]*TeamStrength) []PlayerRow {
	rows := make([]PlayerRow, 0, len(boot.Elements))
	for _, e := range boot.Elements {
		t := teams[e.Team]
		if t == nil || e.Removed {
			continue
		}
		grp := groupFor(e.ElementType)
		r := PlayerRow{
			ID:       e.ID,
			Name:     e.WebName,
			Position: positionNames[e.ElementType],
			Group:    grp,
			TeamID:   e.Team,
			Team:     t.ShortName,
			Cost:     float64(e.NowCost) / 10,
			Points:   e.TotalPoints,
			Form:     atof(e.Form),
			PPG:      atof(e.PointsPerGame),
			Minutes:  e.Minutes,
			Starts:   e.Starts,
			Goals:    e.GoalsScored,
			Assists:  e.Assists,
			CS:       e.CleanSheets,
			XG:       atof(e.ExpectedGoals),
			XA:       atof(e.ExpectedAssists),
			DefCon:   e.DefensiveContribution,
			Bonus:    e.Bonus,
			Selected: atof(e.SelectedByPercent),
			EPNext:   atof(e.EPNext),
			Status:   e.Status,
			Chance:   -1,
			News:     e.News,
		}
		if e.ChanceOfPlayingNextRound != nil {
			r.Chance = *e.ChanceOfPlayingNextRound
		}
		var sum float64
		for _, f := range t.Fixtures {
			if grp == GroupDefence {
				sum += f.DefScore
			} else {
				sum += f.AttScore
			}
			r.Fixtures++
		}
		if r.Fixtures > 0 {
			r.FixScore = round1(sum / float64(r.Fixtures))
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Points > rows[j].Points })
	return rows
}

// ── the inputs ────────────────────────────────────────────────────────

func matchesPlayed(fixtures []Fixture) map[int]int {
	played := map[int]int{}
	for _, f := range fixtures {
		if f.Finished {
			played[f.TeamH]++
			played[f.TeamA]++
		}
	}
	return played
}

// defenceRates reads a club's defensive record off its first-choice
// keeper. Every outfield player's per-90 concession numbers are diluted by
// the minutes they did not play; the keeper who played most of them has,
// by definition, the club's own rate.
func defenceRates(boot *Bootstrap, played map[int]int) (xgc, gc map[int]float64) {
	xgc, gc = map[int]float64{}, map[int]float64{}
	best := map[int]Element{}
	for _, e := range boot.Elements {
		if e.ElementType != 1 || e.Minutes == 0 {
			continue
		}
		if cur, ok := best[e.Team]; !ok || e.Minutes > cur.Minutes {
			best[e.Team] = e
		}
	}
	for _, t := range boot.Teams {
		k, ok := best[t.ID]
		if !ok || played[t.ID] == 0 {
			continue
		}
		xgc[t.ID] = k.ExpectedGoalsConcededPer90
		gc[t.ID] = k.GoalsConcededPer90
	}
	return xgc, gc
}

// attackRates totals the squad's expected goals for chances created, and
// reads goals actually scored off the finished fixtures rather than the
// element list, so own goals land against the right club.
func attackRates(boot *Bootstrap, fixtures []Fixture, played map[int]int) (xg, gf map[int]float64) {
	xg, gf = map[int]float64{}, map[int]float64{}
	totalXG := map[int]float64{}
	for _, e := range boot.Elements {
		totalXG[e.Team] += atof(e.ExpectedGoals)
	}
	goals := map[int]int{}
	for _, f := range fixtures {
		if !f.Finished || f.TeamHScore == nil || f.TeamAScore == nil {
			continue
		}
		goals[f.TeamH] += *f.TeamHScore
		goals[f.TeamA] += *f.TeamAScore
	}
	for _, t := range boot.Teams {
		n := played[t.ID]
		if n == 0 {
			continue
		}
		xg[t.ID] = totalXG[t.ID] / float64(n)
		gf[t.ID] = float64(goals[t.ID]) / float64(n)
	}
	return xg, gf
}

// squadValues rates a squad by the combined price of its fifteen most
// expensive players. Price is the market's running verdict on a squad and
// it is available in week one, which the alternatives - points, form, the
// league table - are not.
func squadValues(boot *Bootstrap) map[int]float64 {
	byTeam := map[int][]int{}
	for _, e := range boot.Elements {
		if e.Removed {
			continue
		}
		byTeam[e.Team] = append(byTeam[e.Team], e.NowCost)
	}
	out := map[int]float64{}
	for id, costs := range byTeam {
		sort.Sort(sort.Reverse(sort.IntSlice(costs)))
		if len(costs) > 15 {
			costs = costs[:15]
		}
		sum := 0
		for _, c := range costs {
			sum += c
		}
		out[id] = float64(sum) / 10
	}
	return out
}

type injury struct {
	burden float64
	names  []string
}

// injuryBurden weighs an absence by how much of the club's season the
// missing player has actually played. That deliberately gives no weight to
// someone who left in the summer or has been out since August: the club's
// attacking and defensive rates above were earned without them, so
// counting them again would penalise the same absence twice.
func injuryBurden(boot *Bootstrap) map[int]injury {
	total := map[int]int{}
	for _, e := range boot.Elements {
		total[e.Team] += e.Minutes
	}
	out := map[int]injury{}
	for _, e := range boot.Elements {
		if e.Status == "a" || e.Minutes == 0 || total[e.Team] == 0 {
			continue
		}
		// An unflagged absence is treated as a certainty; a flagged one is
		// discounted by the chance the club has reported.
		miss := 1.0
		if e.ChanceOfPlayingNextRound != nil {
			miss = 1 - float64(*e.ChanceOfPlayingNextRound)/100
		}
		if miss <= 0 {
			continue
		}
		cur := out[e.Team]
		cur.burden += miss * float64(e.Minutes) / float64(total[e.Team])
		cur.names = append(cur.names, e.WebName)
		out[e.Team] = cur
	}
	return out
}

// lastKickoff finds each club's most recent kickoff before the run starts,
// so the first fixture in the run has a turnaround to measure.
func lastKickoff(fixtures []Fixture, before int) map[int]time.Time {
	out := map[int]time.Time{}
	for _, f := range fixtures {
		if f.Event >= before || f.KickoffTime.IsZero() {
			continue
		}
		for _, id := range []int{f.TeamH, f.TeamA} {
			if prev, ok := out[id]; !ok || f.KickoffTime.After(prev) {
				out[id] = f.KickoffTime
			}
		}
	}
	return out
}

// nextEvent is the gameweek the run should start at: the next one if there
// is one, else the current one, which is what you want mid-gameweek.
func nextEvent(boot *Bootstrap) int {
	for _, e := range boot.Events {
		if e.IsNext {
			return e.ID
		}
	}
	if e, ok := boot.CurrentEvent(); ok {
		return e.ID
	}
	return 1
}

// isMidweek is Tuesday through Thursday in UK time, which is where the
// fixtures actually sit - a Monday night game is a normal round played
// late, not a second match in a week.
func isMidweek(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	switch t.UTC().Weekday() {
	case time.Tuesday, time.Wednesday, time.Thursday:
		return true
	}
	return false
}

// ── small numeric helpers ─────────────────────────────────────────────

// shrinkAll pulls each club's rate toward the league mean in proportion to
// how little evidence there is for it.
func shrinkAll(raw map[int]float64, leagueMean float64, played map[int]int) map[int]float64 {
	out := make(map[int]float64, len(raw))
	for id, v := range raw {
		n := float64(played[id])
		out[id] = (v*n + leagueMean*shrinkPrior) / (n + shrinkPrior)
	}
	return out
}

func mean(m map[int]float64) float64 {
	if len(m) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range m {
		sum += v
	}
	return sum / float64(len(m))
}

func stddev(m map[int]float64) float64 {
	if len(m) < 2 {
		return 0
	}
	mu := mean(m)
	sum := 0.0
	for _, v := range m {
		sum += (v - mu) * (v - mu)
	}
	return math.Sqrt(sum / float64(len(m)))
}

func span(m map[int]float64) (lo, hi float64) {
	first := true
	for _, v := range m {
		if first {
			lo, hi, first = v, v, false
			continue
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return lo, hi
}

// z divides by the spread, and returns 0 rather than infinity when a
// division has no spread at all - week one, before any match is played.
func z(delta, sd float64) float64 {
	if sd == 0 {
		return 0
	}
	return clamp(delta/sd, -zCap, zCap)
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func atof(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}
