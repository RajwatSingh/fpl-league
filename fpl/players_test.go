package fpl

import (
	"math"
	"testing"
	"time"
)

// A two-club division is enough to exercise every term, and small enough
// that the expected sign of each one can be reasoned about by hand.
// Club 1 is rich and tight at the back; club 2 is cheap and leaky.
func testBoot() *Bootstrap {
	chance := func(n int) *int { return &n }
	return &Bootstrap{
		Events: []Event{
			{ID: 1, IsCurrent: true},
			{ID: 2, IsNext: true},
			{ID: 3}, {ID: 4},
		},
		Teams: []Team{
			{ID: 1, Name: "Rich", ShortName: "RCH"},
			{ID: 2, Name: "Poor", ShortName: "POO"},
		},
		Elements: []Element{
			// Rich: expensive squad, a keeper conceding little, good xG.
			{ID: 1, WebName: "Keeper1", ElementType: 1, Team: 1, NowCost: 60, Minutes: 90,
				GoalsConcededPer90: 0.0, ExpectedGoalsConcededPer90: 0.5, Status: "a"},
			{ID: 2, WebName: "Star", ElementType: 4, Team: 1, NowCost: 140, Minutes: 90,
				ExpectedGoals: "1.20", Status: "a"},
			{ID: 3, WebName: "Crock", ElementType: 3, Team: 1, NowCost: 100, Minutes: 90,
				Status: "i", ChanceOfPlayingNextRound: chance(0)},
			// Poor: cheap squad, a keeper shipping goals, little threat.
			{ID: 4, WebName: "Keeper2", ElementType: 1, Team: 2, NowCost: 40, Minutes: 90,
				GoalsConcededPer90: 3.0, ExpectedGoalsConcededPer90: 2.5, Status: "a"},
			{ID: 5, WebName: "Journeyman", ElementType: 4, Team: 2, NowCost: 45, Minutes: 90,
				ExpectedGoals: "0.10", Status: "a"},
			{ID: 6, WebName: "Gone", ElementType: 3, Team: 2, NowCost: 50, Minutes: 0,
				Status: "u", Removed: true},
		},
	}
}

func kickoff(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func testFixtures() []Fixture {
	h, a := 3, 0
	return []Fixture{
		// GW1, played: Rich 3-0 Poor.
		{ID: 1, Event: 1, TeamH: 1, TeamA: 2, Finished: true, Started: true,
			KickoffTime: kickoff("2026-08-15T14:00:00Z"), TeamHScore: &h, TeamAScore: &a},
		// GW2, a Saturday: Poor at home to Rich.
		{ID: 2, Event: 2, TeamH: 2, TeamA: 1, KickoffTime: kickoff("2026-08-22T14:00:00Z")},
		// GW3, a Wednesday four days later - midweek and a short turnaround.
		{ID: 3, Event: 3, TeamH: 1, TeamA: 2, KickoffTime: kickoff("2026-08-26T19:00:00Z")},
	}
}

func find(t *testing.T, b *PlayerBoard, short string) TeamStrength {
	t.Helper()
	for _, s := range b.Teams {
		if s.ShortName == short {
			return s
		}
	}
	t.Fatalf("no team %q in board", short)
	return TeamStrength{}
}

func TestBoardStartsAtNextGameweek(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	if b.Event != 2 {
		t.Errorf("run starts at GW%d, want the next gameweek (2)", b.Event)
	}
	// The played GW1 fixture must not appear in anybody's run.
	for _, s := range b.Teams {
		for _, f := range s.Fixtures {
			if f.Event < 2 {
				t.Errorf("%s carries GW%d, which is already played", s.ShortName, f.Event)
			}
		}
	}
}

func TestStrongerOpponentIsHarder(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	rich, poor := find(t, b, "RCH"), find(t, b, "POO")
	if rich.SquadRating <= poor.SquadRating {
		t.Fatalf("squad rating: rich %.2f is not above poor %.2f", rich.SquadRating, poor.SquadRating)
	}
	// GW2 is the same match seen from both ends, so the comparison is like
	// for like apart from who the opponent is.
	if poor.Fixtures[0].AttScore <= rich.Fixtures[0].AttScore {
		t.Errorf("facing the rich club scored %.1f, not harder than facing the poor one at %.1f",
			poor.Fixtures[0].AttScore, rich.Fixtures[0].AttScore)
	}
}

func TestAwayIsHarderThanHome(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	rich := find(t, b, "RCH")
	away, home := rich.Fixtures[0], rich.Fixtures[1] // GW2 away, GW3 home
	if away.Home || !home.Home {
		t.Fatalf("fixture venues read %v/%v, want away then home", away.Home, home.Home)
	}
	if away.Venue <= 0 || home.Venue >= 0 {
		t.Errorf("venue terms are away %+.2f, home %+.2f - want away positive and home negative",
			away.Venue, home.Venue)
	}
}

func TestMidweekAndShortRestAddFatigue(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	rich := find(t, b, "RCH")
	sat, wed := rich.Fixtures[0], rich.Fixtures[1]
	if sat.Midweek {
		t.Error("a Saturday fixture is marked midweek")
	}
	if !wed.Midweek {
		t.Error("a Wednesday fixture is not marked midweek")
	}
	if wed.RestDays != 4 {
		t.Errorf("turnaround is %d days, want 4", wed.RestDays)
	}
	if wed.Fatigue <= sat.Fatigue {
		t.Errorf("midweek fatigue %+.2f is not above the Saturday's %+.2f", wed.Fatigue, sat.Fatigue)
	}
	if wed.Fatigue > fdFatigueCap+1e-9 {
		t.Errorf("fatigue %+.2f exceeds its cap of %.2f", wed.Fatigue, fdFatigueCap)
	}
}

// An injury is only counted while the player was actually part of the side.
// Someone who left in the summer has no minutes, so the club's own results
// already reflect their absence and counting them would charge for it twice.
func TestInjuryBurdenIgnoresPlayersWhoNeverPlayed(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	rich, poor := find(t, b, "RCH"), find(t, b, "POO")
	if rich.InjuryBurden <= 0 {
		t.Errorf("an injured ever-present counts %.2f, want a burden above zero", rich.InjuryBurden)
	}
	if poor.InjuryBurden != 0 {
		t.Errorf("a departed player with no minutes counts %.2f, want zero", poor.InjuryBurden)
	}
	// The depleted side faces the harder afternoon, so its own fixture
	// carries a positive injury term and its opponent's a negative one.
	if rich.Fixtures[0].Injury <= 0 || poor.Fixtures[0].Injury >= 0 {
		t.Errorf("injury terms are rich %+.2f, poor %+.2f - want the depleted side penalised",
			rich.Fixtures[0].Injury, poor.Fixtures[0].Injury)
	}
}

// A leaky opponent is a soft touch for a forward and irrelevant to a
// keeper - the two lenses have to disagree, or splitting them bought
// nothing.
func TestAttackAndDefenceLensesDiffer(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	poor := find(t, b, "POO") // facing the rich club
	rich := find(t, b, "RCH") // facing the poor club
	if rich.Fixtures[0].AttMatchup >= 0 {
		t.Errorf("attacking a leaky defence scored %+.2f, want a negative (easier) term",
			rich.Fixtures[0].AttMatchup)
	}
	if poor.Fixtures[0].DefMatchup <= 0 {
		t.Errorf("defending against the better attack scored %+.2f, want a positive (harder) term",
			poor.Fixtures[0].DefMatchup)
	}
}

func TestScoresStayOnScale(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	for _, s := range b.Teams {
		for _, f := range s.Fixtures {
			for _, v := range []float64{f.AttScore, f.DefScore} {
				if v < fdMin-1e-9 || v > fdMax+1e-9 {
					t.Errorf("%s GW%d scored %.1f, outside %.0f-%.0f",
						s.ShortName, f.Event, v, fdMin, fdMax)
				}
			}
		}
	}
}

// The horizon is a gameweek window, not a fixture count: a club with a
// blank keeps its place in the run and simply has one fewer match.
func TestHorizonBoundsTheRun(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 1)
	for _, s := range b.Teams {
		for _, f := range s.Fixtures {
			if f.Event != 2 {
				t.Errorf("a one-gameweek horizon returned GW%d", f.Event)
			}
		}
	}
}

// Every player takes their club's run through their own lens, and the
// average has to be over the matches that are actually there.
func TestPlayerScoreFollowsPositionGroup(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	rich := find(t, b, "RCH")
	var keeper, striker PlayerRow
	for _, p := range b.Players {
		switch p.Name {
		case "Keeper1":
			keeper = p
		case "Star":
			striker = p
		}
	}
	if keeper.Group != GroupDefence || striker.Group != GroupAttack {
		t.Fatalf("position groups read %q/%q, want def/att", keeper.Group, striker.Group)
	}
	if keeper.Fixtures != len(rich.Fixtures) || striker.Fixtures != len(rich.Fixtures) {
		t.Errorf("fixture counts are %d/%d, want %d", keeper.Fixtures, striker.Fixtures, len(rich.Fixtures))
	}
	var att, def float64
	for _, f := range rich.Fixtures {
		att += f.AttScore
		def += f.DefScore
	}
	want := round1(att / float64(len(rich.Fixtures)))
	if math.Abs(striker.FixScore-want) > 1e-9 {
		t.Errorf("striker averaged %.1f, want %.1f", striker.FixScore, want)
	}
	if math.Abs(keeper.FixScore-round1(def/float64(len(rich.Fixtures)))) > 1e-9 {
		t.Errorf("keeper averaged %.1f off the attacking scores", keeper.FixScore)
	}
}

// A player the game has withdrawn has no fixtures ahead of them, and
// showing them would put a name with an empty run in every search.
func TestRemovedPlayersAreDropped(t *testing.T) {
	b := buildBoard(testBoot(), testFixtures(), 10)
	for _, p := range b.Players {
		if p.Name == "Gone" {
			t.Error("a removed player is still on the board")
		}
	}
}

// Shrinkage is what stops three matches reading as a settled truth: a club
// with one game of evidence must sit nearer the league mean than a club
// with ten identical ones.
func TestShrinkagePullsThinEvidenceToTheMean(t *testing.T) {
	raw := map[int]float64{1: 3.0, 2: 3.0}
	got := shrinkAll(raw, 1.0, map[int]int{1: 1, 2: 10})
	if !(got[1] < got[2]) {
		t.Errorf("one match shrank to %.2f and ten to %.2f, want the thinner evidence nearer the mean",
			got[1], got[2])
	}
	if got[2] >= 3.0 {
		t.Errorf("ten matches shrank to %.2f, which is not below the raw 3.0", got[2])
	}
}

// The API returns fixtures by id, which is not promised to be kickoff
// order. Handing them over shuffled must not produce a negative
// turnaround.
func TestRestDaysSurviveUnorderedFixtures(t *testing.T) {
	fx := testFixtures()
	shuffled := []Fixture{fx[2], fx[0], fx[1]}
	b := buildBoard(testBoot(), shuffled, 10)
	for _, s := range b.Teams {
		for _, f := range s.Fixtures {
			if f.RestDays < 0 {
				t.Errorf("%s GW%d has a turnaround of %d days", s.ShortName, f.Event, f.RestDays)
			}
		}
	}
	rich := find(t, b, "RCH")
	if rich.Fixtures[0].Event != 2 || rich.Fixtures[1].Event != 3 {
		t.Errorf("run came back as GW%d,GW%d - want it sorted by kickoff",
			rich.Fixtures[0].Event, rich.Fixtures[1].Event)
	}
}
