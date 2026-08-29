# fpl-league-rank

CLI for inspecting an FPL classic league: bench points, gameweek rank, chips played,
and transfers with their point deductions.

API behaviour this is built on is documented in [FPL_API_NOTES.md](FPL_API_NOTES.md).

## Build

    go build -o fplleague .

Defaults to league **580906** (Gullu League 3.0), so no flag is needed for it.

    ./fplleague -serve :8080          # web UI at http://localhost:8080
    ./fplleague                       # current gameweek
    ./fplleague -gw 1                 # a specific gameweek
    ./fplleague -transfers            # list each transfer by player name
    ./fplleague -season               # season totals + chips used
    ./fplleague -sort bench           # rank, gw, bench, hits, gwrank
    ./fplleague -json                 # machine-readable

    ./fplleague -league 123456        # any other league

`-league` is the number in an FPL league URL. Other flags: `-max` (cap managers
fetched), `-concurrency` (default 6), `-json`.

The default lives in `defaultLeague` (main.go), which the CLI flag and the HTTP
API share, and `DEFAULT_LEAGUE` in web/index.html. Change all three together.

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

## Layout

    fpl/client.go     HTTP client: browser UA, status checks, retry/backoff on 429+5xx
    fpl/types.go      API response types
    fpl/endpoints.go  one function per endpoint, standings paginated
    fpl/report.go     assembles the league report, bounded concurrency
    server.go         HTTP API + embedded web UI, 60s report cache
    web/index.html    the frontend: one file, no build step, no dependencies
    main.go           flags and table rendering
