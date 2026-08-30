package fpl

import (
	"context"
	"fmt"
)

func (c *Client) Bootstrap(ctx context.Context) (*Bootstrap, error) {
	var b Bootstrap
	if err := c.Get(ctx, "bootstrap-static/", &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// ClassicLeague walks every standings page and returns the league metadata plus
// all ranked rows. It stops early once maxManagers rows have been collected
// (maxManagers <= 0 means no limit).
func (c *Client) ClassicLeague(ctx context.Context, leagueID, maxManagers int) (League, []StandingRow, NewEntries, error) {
	var (
		league  League
		rows    []StandingRow
		newEnts NewEntries
	)
	for page := 1; ; page++ {
		var resp LeagueResponse
		path := fmt.Sprintf("leagues-classic/%d/standings/?page_standings=%d", leagueID, page)
		if err := c.Get(ctx, path, &resp); err != nil {
			return league, rows, newEnts, err
		}
		if page == 1 {
			league = resp.League
			newEnts = resp.NewEntries
		}
		rows = append(rows, resp.Standings.Results...)
		if maxManagers > 0 && len(rows) >= maxManagers {
			return league, rows[:maxManagers], newEnts, nil
		}
		if !resp.Standings.HasNext {
			return league, rows, newEnts, nil
		}
	}
}

// EntryHistory returns every gameweek row plus the chips used, in one call.
func (c *Client) EntryHistory(ctx context.Context, entryID int) (*History, error) {
	var h History
	if err := c.Get(ctx, fmt.Sprintf("entry/%d/history/", entryID), &h); err != nil {
		return nil, err
	}
	return &h, nil
}

// EntryTransfers returns every transfer this season, newest first.
func (c *Client) EntryTransfers(ctx context.Context, entryID int) ([]Transfer, error) {
	var t []Transfer
	if err := c.Get(ctx, fmt.Sprintf("entry/%d/transfers/", entryID), &t); err != nil {
		return nil, err
	}
	return t, nil
}

// EntryPicks 404s if the gameweek has not kicked off or the manager did not
// enter it; check with NotFound.
func (c *Client) EntryPicks(ctx context.Context, entryID, gw int) (*PicksResponse, error) {
	var p PicksResponse
	if err := c.Get(ctx, fmt.Sprintf("entry/%d/event/%d/picks/", entryID, gw), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

func (c *Client) EventLive(ctx context.Context, gw int) (*LiveResponse, error) {
	var l LiveResponse
	if err := c.Get(ctx, fmt.Sprintf("event/%d/live/", gw), &l); err != nil {
		return nil, err
	}
	return &l, nil
}

// Fixtures returns every fixture scheduled for the gameweek, in kickoff order.
func (c *Client) Fixtures(ctx context.Context, gw int) ([]Fixture, error) {
	var f []Fixture
	if err := c.Get(ctx, fmt.Sprintf("fixtures/?event=%d", gw), &f); err != nil {
		return nil, err
	}
	return f, nil
}
