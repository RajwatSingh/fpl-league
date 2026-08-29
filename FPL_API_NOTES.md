# FPL API — reference for a league dashboard

All findings below were verified against the live API on **2026-08-28** (2026/27 season, GW2 current).

Base URL: `https://fantasy.premierleague.com/api/`
No auth, no API key, JSON, CORS-blocked in the browser (so call it from a server / proxy).
Send a normal browser `User-Agent`; a missing/odd one can get you blocked.

---

## The 5 endpoints you need

| Endpoint | Gives you | Cost |
|---|---|---|
| `bootstrap-static/` | player list, teams, gameweek metadata, chip definitions | 1 call, ~1.6 MB — cache it |
| `leagues-classic/{league_id}/standings/?page_standings={n}` | league members (50/page) | ceil(N/50) calls |
| `entry/{entry_id}/event/{gw}/picks/` | **squad, bench points, chip, transfer cost for one GW** | 1 call per manager per GW |
| `entry/{entry_id}/history/` | every GW's points/rank/bench/hits + all chips used | 1 call per manager (whole season) |
| `entry/{entry_id}/transfers/` | every individual transfer, all season | 1 call per manager |
| `event/{gw}/live/` | per-player points for a GW (needed only for live/derived math) | 1 call per GW |

`entry_id` comes from the `entry` field in league standings. `league_id` is the number in the FPL league URL.

---

## Feature 1 — Bench points per manager

**Per gameweek:** `entry/{id}/event/{gw}/picks/` → `entry_history.points_on_bench`
**Whole season:** `entry/{id}/history/` → `current[].points_on_bench` (one row per GW — this is the cheap way to do it for every GW at once)

Verified: `points_on_bench` equals the sum of live points for picks where `multiplier == 0`.

Two things that will bite you:

1. **Auto-subs are already baked in.** After a GW finalises, the `picks` array is re-sorted — a player subbed on appears in positions 1–11 with `multiplier: 1`, and the player he replaced is moved to positions 12–15 with `multiplier: 0`. The `automatic_subs` array is informational only; do **not** apply it yourself or you'll double-count.
2. **Bench Boost reports `points_on_bench: 0`.** During `bboost` all 15 picks get `multiplier: 1`, so there is no bench. If you want "what the bench actually scored" during a BB, compute it yourself: take picks at `position > 11` and sum their `total_points` from `event/{gw}/live/`.

```
picks:  { element, position, multiplier, is_captain, is_vice_captain, element_type }
entry_history: { event, points, total_points, rank, overall_rank, bank, value,
                 event_transfers, event_transfers_cost, points_on_bench }
```

---

## Feature 2 — Gameweek rank

Careful: there are two different ranks and the field names are confusing.

- `rank` = **rank in that gameweek** (out of ~11M) ← this is what you want
- `overall_rank` = cumulative season rank after that GW

Both live in `entry/{id}/history/` → `current[]`, and in `picks/` → `entry_history`.
`entry/{id}/` also exposes `summary_event_rank` and `summary_overall_rank` for the latest GW only.

Also present: `percentile_rank` (integer bucket) and `overall_rank_percentage` (string).

For **rank within your league**, use the standings endpoint: `rank`, `last_rank` (previous GW's position), `rank_sort` (tiebreak ordering).

⚠️ GW rank is only populated once the GW's data is checked. Watch `events[].data_checked` in `bootstrap-static/`.

---

## Feature 3 — Chips played

**Season view (best):** `entry/{id}/history/` → `chips: [{ name, time, event }]`
**Single GW:** `entry/{id}/event/{gw}/picks/` → `active_chip` (string or `null`)

Chip name strings: `wildcard`, `freehit`, `bboost` (Bench Boost), `3xc` (Triple Captain).

Since the two-half chip rework, `bootstrap-static/` has a top-level `chips` array defining each chip *instance* with `start_event` / `stop_event` — e.g. wildcard #1 is GWs 2–19, wildcard #2 is GWs 20–38, and `chip_type` is `"transfer"` or `"team"`. Use this if you want to show "chips remaining" rather than just "chips used", since a manager gets each chip twice.

`bootstrap-static/` → `events[].chip_plays` also gives league-wide counts per chip per GW, useful as a benchmark column.

---

## Feature 4 — Transfers and point deductions

**Counts per GW:** `entry/{id}/history/` → `current[].event_transfers` and `current[].event_transfers_cost` (the hit, already a positive number like 4, 8, 12).

**Individual transfers:** `entry/{id}/transfers/` returns a flat array, newest first, for the whole season:
```
{ element_in, element_in_cost, element_out, element_out_cost, entry, event, time }
```
Costs are in tenths (65 = £6.5m). Join `element_in`/`element_out` against `bootstrap-static/` → `elements[]` by `id` to get `web_name`. Group by `event` to render "GW5: Salah → Palmer (-4)".

### ⚠️ The important gotcha: `points` is GROSS, not net

This is the single thing most easily gotten wrong. Verified on multiple entries:

- `history.current[].points` — **does NOT** subtract the hit
- `picks.entry_history.points` — **does NOT** subtract the hit
- `entry/{id}/` → `summary_event_points` — **does NOT** subtract the hit
- league standings → `event_total` — **does NOT** subtract the hit
- `history.current[].total_points` — **IS** net (cumulative, hits already deducted)
- league standings → `total` — **IS** net

Worked example (entry 7844490, GW2): `points = 33`, `event_transfers_cost = 12`, standings `event_total = 33`, but `total` moved by only 21.

So your GW column must be:
```
net_gw_points = points - event_transfers_cost
```

And to sanity-check any GW:
```
sum(live_points[pick.element] * pick.multiplier for pick in picks) == entry_history.points
```
(verified exact — captain doubling and auto-subs included)

---

## Gameweek metadata — `bootstrap-static/` → `events[]`

```
id, name, deadline_time, deadline_time_epoch,
is_previous, is_current, is_next,
finished, data_checked,
average_entry_score, highest_score, highest_scoring_entry, ranked_count,
chip_plays[], most_captained, most_selected, most_transferred_in, top_element_info
```

- `is_current` → the GW to default your UI to
- `finished` → all matches done; `data_checked` → bonus applied and ranks final. Only trust ranks and bench points once `data_checked` is true.

Also in `bootstrap-static/`: `elements[]` (players — `id`, `web_name`, `element_type`, `team`, `now_cost`, `event_points`, `total_points`, plus xG/xA and the new `defensive_contribution`), `teams[]`, `element_types[]` (GKP/DEF/MID/FWD), and `phases[]` (monthly leaderboard windows).

---

## Practical notes for building this

**Standings pagination.** 50 rows per page; loop `page_standings` while `standings.has_next` is true. Response also carries `new_entries` (managers who joined mid-season and aren't ranked yet) — handle it or they'll silently vanish from your table. Deep paging works fine (I pulled page 145,904 of the Overall league).

Optional `&phase={n}` gives monthly standings — phase ids come from `bootstrap-static/` → `phases[]` (1 = Overall, 2 = August, 3 = September…). The row shape differs slightly (includes `id` and `has_played`).

**Request volume.** A 40-manager league, current GW only ≈ 1 bootstrap + 1 standings + 40 picks = 42 calls. Full season history for the same league = 40 more (history) + 40 (transfers). `history/` covers every GW in one call, so **prefer `history/` over looping `picks/` per GW** — only fetch `picks/` for the GW whose actual squad you're displaying.

**Caching.** Everything is immutable once `data_checked` is true for that GW — cache finished GWs permanently, and cache the current GW for ~60s during matches. `bootstrap-static/` changes at most a few times a day (prices at ~01:30 UK).

**Concurrency.** Rate limiting is undocumented. I made a few hundred sequential requests without a 429, but keep concurrency low (5–10) and add retry-with-backoff on 429/5xx.

**Error cases (verified):**
- `entry/{id}/event/{gw}/picks/` → **404** if the GW hasn't kicked off yet (deadline not passed) or the manager didn't enter that GW. GW3 picks 404 right now. Handle this — don't crash on the upcoming GW.
- `entry/{id}/` → 404 for a nonexistent entry.
- A manager who joined at GW5 has `started_event: 5` and `entered_events: [5,6,...]` on `entry/{id}/` — use it to skip GWs that will 404.

**H2H leagues** (if your league is H2H rather than classic) use `leagues-h2h/{id}/standings/` and `leagues-h2h-matches/league/{id}/?page={n}`. I couldn't verify these — I had no valid H2H league id to test against.

---

## Suggested fetch order

1. `bootstrap-static/` → cache; find `is_current` GW, build player id → name map
2. `leagues-classic/{id}/standings/` (paginate) → list of `entry` ids
3. Per entry, in parallel:
   - `entry/{e}/history/` → bench points, GW rank, hits, chips for every GW
   - `entry/{e}/transfers/` → transfer detail (only if you want in/out names)
4. `entry/{e}/event/{current}/picks/` only for the GW you're displaying squads for
5. `event/{gw}/live/` only if you need live in-progress scores or true Bench-Boost bench points

Steps 1–3 alone cover all four of your features for the whole season.
