package fpl

import "context"

// TeamPlayer is one pick enriched with identity, this gameweek's live score,
// and how far along their club's fixture is.
type TeamPlayer struct {
	Element       int    `json:"element"`
	Name          string `json:"name"`
	Position      string `json:"position"` // GKP / DEF / MID / FWD
	Team          string `json:"team"`     // club short name
	Multiplier    int    `json:"multiplier"`
	IsCaptain     bool   `json:"isCaptain"`
	IsViceCaptain bool   `json:"isViceCaptain"`
	Points        int    `json:"points"`
	// FixtureStatus is "upcoming", "live", "finished" or "none" (blank
	// gameweek for that club).
	FixtureStatus string `json:"fixtureStatus"`
}

// TeamDetail is a manager's full squad for one gameweek: who they picked, who
// they captained, and how many of the players that still count towards their
// score haven't finished playing yet.
type TeamDetail struct {
	Entry    int          `json:"entry"`
	Event    int          `json:"event"`
	Chip     string       `json:"chip"`
	Starters []TeamPlayer `json:"starters"`
	Bench    []TeamPlayer `json:"bench"`
	Captain  *TeamPlayer  `json:"captain"`
	Vice     *TeamPlayer  `json:"vice"`

	// Counts are over the starting XI, except on a Bench Boost where the
	// bench counts too - that's the same set of players whose fixtures can
	// still add to the gameweek score.
	ToPlay   int `json:"toPlay"`
	Live     int `json:"live"`
	Finished int `json:"finished"`
	OfTotal  int `json:"ofTotal"`
}

var positionNames = map[int]string{1: "GKP", 2: "DEF", 3: "MID", 4: "FWD"}

// BuildTeamDetail fetches everything needed to show one manager's squad for
// one gameweek: their picks, the live score for every player in the game,
// and every fixture that gameweek, so each pick can be marked by how far
// along their club's match is.
func (c *Client) BuildTeamDetail(ctx context.Context, entryID, gw int) (*TeamDetail, error) {
	boot, err := c.Bootstrap(ctx)
	if err != nil {
		return nil, err
	}
	picks, err := c.EntryPicks(ctx, entryID, gw)
	if err != nil {
		return nil, err
	}
	live, err := c.EventLive(ctx, gw)
	if err != nil {
		return nil, err
	}
	fixtures, err := c.Fixtures(ctx, gw)
	if err != nil {
		return nil, err
	}

	elements := make(map[int]Element, len(boot.Elements))
	for _, e := range boot.Elements {
		elements[e.ID] = e
	}
	teams := make(map[int]string, len(boot.Teams))
	for _, t := range boot.Teams {
		teams[t.ID] = t.ShortName
	}
	points := live.PointsByElement()
	statusByTeam := teamFixtureStatus(fixtures)

	det := &TeamDetail{Entry: entryID, Event: gw, Chip: ChipLabel(picks.ActiveChip)}

	for _, p := range picks.Picks {
		el := elements[p.Element]
		raw := points[p.Element]
		pts := raw * p.Multiplier
		// A bench pick's multiplier is 0 (unless boosted), which would show
		// as 0 points regardless of how the player actually did - exactly
		// the number a manager wants to see to know what they left out.
		if p.Position > 11 && p.Multiplier == 0 {
			pts = raw
		}
		tp := TeamPlayer{
			Element:       p.Element,
			Name:          el.WebName,
			Position:      positionNames[el.ElementType],
			Team:          teams[el.Team],
			Multiplier:    p.Multiplier,
			IsCaptain:     p.IsCaptain,
			IsViceCaptain: p.IsViceCaptain,
			Points:        pts,
			FixtureStatus: statusByTeam[el.Team],
		}
		if p.IsCaptain {
			cp := tp
			det.Captain = &cp
		}
		if p.IsViceCaptain {
			vp := tp
			det.Vice = &vp
		}
		if p.Position <= 11 {
			det.Starters = append(det.Starters, tp)
		} else {
			det.Bench = append(det.Bench, tp)
		}
	}

	relevant := det.Starters
	if picks.ActiveChip == "bboost" {
		relevant = append(append([]TeamPlayer{}, det.Starters...), det.Bench...)
	}
	det.OfTotal = len(relevant)
	for _, p := range relevant {
		switch p.FixtureStatus {
		case "live":
			det.Live++
		case "finished", "none":
			det.Finished++
		default:
			det.ToPlay++
		}
	}

	return det, nil
}

// teamFixtureStatus collapses every fixture into one status per club. A club
// with a fixture still in progress is "live" even if it has another fixture
// already finished (a double gameweek); a club with nothing left to kick off
// is "finished" only once every one of its fixtures has finished.
func teamFixtureStatus(fixtures []Fixture) map[int]string {
	type agg struct{ live, upcoming, finished bool }
	byTeam := map[int]*agg{}
	touch := func(id int, f Fixture) {
		a, ok := byTeam[id]
		if !ok {
			a = &agg{}
			byTeam[id] = a
		}
		switch {
		case f.Finished:
			a.finished = true
		case f.Started:
			a.live = true
		default:
			a.upcoming = true
		}
	}
	for _, f := range fixtures {
		touch(f.TeamH, f)
		touch(f.TeamA, f)
	}
	out := make(map[int]string, len(byTeam))
	for id, a := range byTeam {
		switch {
		case a.live:
			out[id] = "live"
		case a.upcoming:
			out[id] = "upcoming"
		case a.finished:
			out[id] = "finished"
		}
	}
	return out
}
