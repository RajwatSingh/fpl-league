# fpl-league-rank

CLI for inspecting an FPL classic league: bench points, gameweek rank, chips played,
and transfers with their point deductions. The web UI adds a second section that
rates every player's next ten fixtures for difficulty.

API behaviour this is built on is documented in [FPL_API_NOTES.md](FPL_API_NOTES.md).

## Build

    go build -o fplleague .

Defaults to league **580906** (Gullu League 3.0), so no flag is needed for it.

    ./fplleague -serve :8080          # web UI at http://localhost:8080
    ./fplleague                       # current gameweek
    ./fplleague -gw 1                 # a specific gameweek
    ./fplleague -transfers            # list each transfer by player name
    ./fplleague -season               # season totals + chips used
    ./fplleague -sort bench           # rank, gw, bench, hits, gwrank, wins, benchwins
    ./fplleague -json                 # machine-readable

    ./fplleague -league 123456        # any other league

`-league` is the number in an FPL league URL. Other flags: `-max` (cap managers
fetched), `-concurrency` (default 6), `-json`.

The default lives in `defaultLeague` (main.go), which the CLI flag and the HTTP
API share, and `DEFAULT_LEAGUE` in web/index.html. Change all three together.

## Weekly honours

Two tallies, both computed from data already fetched — no extra API calls.

- **★ WIN** — highest score that gameweek, **net of hits**. That is the figure the
  league table actually moves by, and the one a manager would argue about.
- **▬ BENCH** — most points left on the bench that gameweek.

Ties are shared: if two managers post the same top score, both get the win.

**Monthly standings** use FPL's own calendar-month "phases" (August, September, …
from `bootstrap-static` → `phases[]`), scored by summing each manager's net
points across the gameweeks in that phase — again from data already fetched, no
extra calls. The season-long phase (always named "Overall") is excluded by
name rather than id, since the API does not guarantee phase 1 stays the
season. A month is marked `provisional` until its last gameweek is
`data_checked`, same rule as the gameweek view. `-monthly` on the CLI, a third
"Monthly" tab in the web UI, and a `monthWins` tally on the season view.

A Bench Boost week cannot win the bench tally. The bench played, so nothing was
left behind, and `points_on_bench` is 0 — which is the honest answer. Note this
produces a display that looks contradictory until you know the rule: in GW1 of
Gullu League, Ashav Shrestha shows a bench of `22^` and no bench badge, while
Rajan Thapa shows `22` and takes it. The first 22 was scored by a boosted bench
and counted; the second was genuinely thrown away.

## Two things the output accounts for

**GW is net of the hit.** The API's `points` field is gross — it does not subtract
`event_transfers_cost`, and neither does the league standings `event_total`. The GW
column here subtracts it, so it matches what the season total actually moves by.

**Bench points during a Bench Boost.** The API reports `points_on_bench: 0` for a BB
week because all 15 picks count. Where that happens the gameweek view recomputes the
real bench from `picks` + `event/{gw}/live/` and marks it with `*`. The `-season`
view does not (it would cost an extra two calls per manager per BB week), so its
BENCHED column reads 0 for those weeks.

## Web UI

`-serve` runs the same report behind an HTTP API and serves a single embedded
page against it, so the browser and the CLI share one implementation of the
gross/net and Bench Boost rules rather than each having their own. Reports are
cached for 60s so a page refresh does not re-run dozens of upstream calls.

Design follows the palette and type contract in `../rajwat-singh/portfolio`.
One note on encoding: that palette holds its accents at near-constant lightness
and separates them by hue, which is right for a syntax theme and wrong for a
chart. Run through a CVD check, blue and purple are 0.4 ΔE apart for a
deuteranope and green and rose are 4.6. So nothing in the UI encodes a value by
hue alone — the bench meter is one hue varying in length, chips carry their
two-letter label, and rank movement carries an arrow with the colour as a
second channel.

The portfolio renders nothing when the FPL API is down, on the grounds that no
reader of a portfolio needs to know. This inverts that: someone here is trying
to get an answer, so a failure shows the actual upstream reason.

### This week, at the top

The masthead used to be a 110px band carrying a league name, a season, a
gameweek and a manager count — three of them ambient, none of them the reason
anyone opened the page. It now answers the three questions a league of friends
actually argues about, and answers them in **names rather than metrics**: who
won the week, who is top, who left the most behind. Ties are shared, so a field
names the holder with the best figure and marks the others `+n`.

Three fields of one band, split by the rule the masthead already had — not
three cards. No shadow, no radius, no border of their own; the only accent is a
left edge in each honour's own hue, which is the leader's gilt edge idiom the
table has used all along.

The pitch's centre circle used to be drawn across this space. It was right when
the band was mostly air, and wrong the moment three people's names were sitting
under it, so it was removed rather than shrunk. The favicon still carries the
mark.

### The table's shape is a fact about the week

A column every row leaves empty is not a column — it is 76px of middle dots
holding the table open. Nobody took a hit this week, so there is no Hit column;
the gameweek has finished, so there is no To play. On a typical settled week
that takes ten columns down to eight. Sorting by a dropped column brings it
back, since a table that hid the column you asked to sort by would be arguing
with the control that built it.

The table is `width: 100%` with short columns, and the browser used to share
the slack out evenly: measured at 1380px, the content came to 560px and the
other 650 became gaps — `Hit` got 76px to hold a middle dot. Every column
floated alone in its own pool and the eye had to cross a void to get from a
team's name to its score. Team now absorbs all of it and every other column
shrinks to its content, so the numbers close up into a block that can be
compared down the page.

### Two shapes, one set of rows

Above 640px the report is the ten-column sortable table. Below it, the same
rows render as a **ladder** — rank, identity, the two numbers, and a rail
underneath carrying everything the columns used to. The table cannot honestly
fit a phone: ten columns on a 390px screen put `TOTAL`, the one number a league
table exists to report, tenth from the left and five columns into a sideways
scroll.

Both shapes are built from the same row objects and the same helpers, so they
cannot disagree about a value, a badge or a hue. Only the arrangement differs.

The rail has two registers, and the split carries meaning. A **fact** — captain,
bench, transfers, players still to play — is true of every manager every week and
reads as a plain icon and a value. A **badge** — gameweek win, most benched,
chip, hit — is something that happened to this one, and gets a fill. So a row is
as tall as its week was eventful: a quiet manager is two lines, and the one who
won the week on a −12 hit with a Triple Captain runs to four.

Icons are objects from the game rather than UI metaphors — the armband, the
substitution board, the referee's card, the bench, the clock — because the page
has one subject and its readers already know that vocabulary. There are five,
and the rule for adding a sixth is that it must replace something the layout is
currently carrying badly. Every badge still keeps its word or its number: the
palette note above is why nothing here is identified by hue alone.

Mobile loses the table's sortable column headings, which is most of how the page
gets used, so the ladder's two numbers carry theirs as tap targets. The rest of
the sorts stay in the filters sheet.

## Players & fixtures

A second section of the web UI, switched from the nav under the masthead. It
rates every club's next ten gameweeks for difficulty and lists every player's
season alongside their own run. `/api/players?horizon=10`; three upstream calls
regardless of how many players come back, because the whole model is derived
from `bootstrap-static/` plus the full fixture list. Cached 15 minutes — fixtures
and prices move on the scale of hours, not the 60 seconds a live gameweek does.

**Difficulty runs 1 (easiest) to 10 (hardest)**, from a neutral 5.5 plus five
signed terms. They are kept separate rather than collapsed into one weight
vector because the dialog shows the breakdown: an 8.4 has to be readable as
"away at a top squad", not taken on faith.

| Term | Range | What it reads |
|---|---|---|
| Opponent squad | ±1.6 | Combined price of the club's 15 most expensive players, placed on a 0–1 scale across the league |
| Matchup | ±1.5 | Opponent's chances given (xGC) and goals conceded for an attacker; their xG and goals scored for a defender |
| Venue | ±0.7 | Home or away, flat — roughly the third of a goal home advantage is worth |
| Fatigue | 0 to +0.9 | A Tuesday–Thursday kickoff, plus a turnaround of four days or less; they stack |
| Injuries | ±0.8 | Opponent's absences minus your own, so two equally depleted squads cancel |

Three things the model does deliberately:

**It is position-aware.** A forward's fixture is easy when the opponent's defence
leaks; a defender's is easy when their attack is toothless. Scoring both off
"how good is the opponent" would hand a keeper the same number as the striker in
front of him, so each fixture carries two scores and a player reads the one for
their own job.

**Rates are shrunk toward the league mean** as though every club had already
played six average matches. Three gameweeks in, the unshrunk numbers say
Sunderland have the best defence in England.

**An absence only counts while the player was in the side.** Weight is the share
of the club's minutes so far that belongs to whoever is now unavailable,
discounted by the reported chance of playing. Someone who left in the summer, or
has been out since August, scores zero — the club's attacking and defensive
rates above were earned without them, and counting them again would charge for
the same absence twice.

The defensive record is read off each club's first-choice keeper. Every outfield
player's per-90 concession numbers are diluted by the minutes they did not play;
the keeper who played most of them has, by definition, the club's own rate.

`teams[].strength_attack_*` and `strength_defence_*` are not usable — the API
returns 0 for all twenty (see the API notes) — which is why squad rating is
built from price instead.

### The ticker

Ten gameweeks in fixed columns, because the value of a ticker is that a run
reads down the page as well as across it: GW7 has to sit under GW7 for every
player, or comparing two of them means reading two sentences instead of one
shape. A blank keeps its column and shows `—`; a double stacks both matches
rather than averaging them, since two fixtures is the fact that matters most
about that week.

The difficulty ramp is the one place in the UI that is a scale rather than a
syntax theme, so unlike the accent set it is ordered by lightness as well as
hue — read in greyscale, step 1 is still the palest and step 5 the deepest. It
still never carries a value alone: every cell prints its own score, and a
midweek round is marked with a notch rather than a sixth colour.

The phone gets the same rows as cards with the run as its own scrolling strip.
The fixed columns are exactly what a 390px screen cannot hold, and what is
gained is that one player's run is fully legible without pinching.

## Layout

    fpl/client.go     HTTP client: browser UA, status checks, retry/backoff on 429+5xx
    fpl/types.go      API response types
    fpl/endpoints.go  one function per endpoint, standings paginated
    fpl/report.go     assembles the league report, bounded concurrency
    fpl/players.go    the fixture-difficulty model and the player board
    fpl/players_test.go  the model's terms, each pinned by sign rather than value
    server.go         HTTP API + embedded web UI, 60s report cache, 15min player board
    web/index.html    the frontend: one file, no build step, no dependencies
    main.go           flags and table rendering
