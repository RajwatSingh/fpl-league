package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"fplleague/fpl"
)

// defaultLeague is Gullu League 3.0 — the league this tool is pointed at
// unless something says otherwise. Also the fallback for the web UI and the
// HTTP API, so every entry point agrees on one default.
const defaultLeague = 580906

func main() {
	var (
		leagueID    = flag.Int("league", defaultLeague, "classic league id (from the FPL league URL)")
		gw          = flag.Int("gw", 0, "gameweek to report on (default: current)")
		season      = flag.Bool("season", false, "show season totals instead of a single gameweek")
		detail      = flag.Bool("transfers", false, "list each transfer with player names")
		sortBy      = flag.String("sort", "rank", "sort by: rank, gw, bench, hits, gwrank")
		maxManagers = flag.Int("max", 0, "cap the number of managers fetched (0 = all)")
		concurrency = flag.Int("concurrency", 6, "simultaneous API requests")
		asJSON      = flag.Bool("json", false, "emit JSON instead of a table")
		serveAddr   = flag.String("serve", "", "run the web UI on this address, e.g. :8080")
	)
	flag.Parse()

	if *serveAddr != "" {
		if err := serve(*serveAddr, *concurrency); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *leagueID <= 0 {
		fmt.Fprintln(os.Stderr, "usage: fplleague [-league <id>] [-gw n] [-season] [-transfers]")
		flag.PrintDefaults()
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	start := time.Now()
	client := fpl.NewClient()
	rep, err := client.BuildReport(ctx, *leagueID, fpl.Options{
		GW:          *gw,
		MaxManagers: *maxManagers,
		Concurrency: *concurrency,
		WithDetail:  *detail || *season,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if *season {
		printSeason(rep, *sortBy)
	} else {
		printGameweek(rep, *sortBy, *detail)
	}
	fmt.Fprintf(os.Stderr, "\n(%d managers in %s)\n", len(rep.Gameweek), time.Since(start).Round(time.Millisecond))
}

func printGameweek(rep *fpl.Report, sortBy string, detail bool) {
	fmt.Printf("%s  —  %s\n", rep.League.Name, rep.Event.Name)
	if rep.Provisional {
		fmt.Println("Provisional: this gameweek is not finalised, so ranks, bonus and bench points can still move.")
	}
	if rep.NewEntries > 0 {
		fmt.Printf("%d manager(s) joined recently and are not yet ranked.\n", rep.NewEntries)
	}
	fmt.Println()

	rows := append([]fpl.ManagerGW(nil), rep.Gameweek...)
	sortGameweek(rows, sortBy)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tMANAGER\tTEAM\tGW\tHIT\tBENCH\tCHIP\tTF\tGW RANK\tTOTAL")
	for _, r := range rows {
		if !r.Played {
			fmt.Fprintf(w, "%d\t%s\t%s\t—\t—\t—\t—\t—\t—\t%d\n",
				r.LeagueRank, trunc(r.Manager, 20), trunc(r.Entry, 20), r.Total)
			continue
		}
		bench := fmt.Sprintf("%d", r.Bench)
		if r.BenchDerived {
			bench += "*"
		}
		hit := "-"
		if r.Hit > 0 {
			hit = fmt.Sprintf("-%d", r.Hit)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\t%s\t%d\t%s\t%d\n",
			r.LeagueRank, trunc(r.Manager, 20), trunc(r.Entry, 20),
			r.NetPoints, hit, bench, dash(r.Chip), r.Transfers, comma(r.GWRank), r.Total)
	}
	w.Flush()

	fmt.Println("\nGW is net of the transfer hit. * = bench recomputed because Bench Boost reports 0.")

	if detail {
		printTransferDetail(rep, rows)
	}
}

func printTransferDetail(rep *fpl.Report, rows []fpl.ManagerGW) {
	fmt.Printf("\nTransfers in %s\n\n", rep.Event.Name)
	any := false
	for _, r := range rows {
		if len(r.TransferDetail) == 0 {
			continue
		}
		any = true
		hit := ""
		if r.Hit > 0 {
			hit = fmt.Sprintf("  (-%d)", r.Hit)
		}
		fmt.Printf("  %s%s\n", r.Manager, hit)
		for _, t := range r.TransferDetail {
			fmt.Printf("      %s → %s\n",
				rep.PlayerNames[t.ElementOut], rep.PlayerNames[t.ElementIn])
		}
	}
	if !any {
		fmt.Println("  (none)")
	}
}

func printSeason(rep *fpl.Report, sortBy string) {
	fmt.Printf("%s  —  season to %s\n\n", rep.League.Name, rep.Event.Name)

	rows := append([]fpl.ManagerSeason(nil), rep.Season...)
	sortSeason(rows, sortBy)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tMANAGER\tTEAM\tTOTAL\tBENCHED\tHITS\tTF\tBEST GW\tCHIPS USED")
	for _, r := range rows {
		best := "—"
		if r.BestGW > 0 {
			best = fmt.Sprintf("%d (GW%d)", r.BestGWPoints, r.BestGW)
		}
		hits := "-"
		if r.HitsTotal > 0 {
			hits = fmt.Sprintf("-%d", r.HitsTotal)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%d\t%s\t%d\t%s\t%s\n",
			r.LeagueRank, trunc(r.Manager, 20), trunc(r.Entry, 20),
			r.Total, r.BenchTotal, hits, r.TransfersTotal, best, chipList(r.ChipsUsed))
	}
	w.Flush()
	fmt.Println("\nBENCHED is season-long points left on the bench (0 for any Bench Boost week).")
}

func chipList(chips []fpl.ChipPlay) string {
	if len(chips) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(chips))
	for _, c := range chips {
		parts = append(parts, fmt.Sprintf("%s(%d)", fpl.ChipLabel(c.Name), c.Event))
	}
	return strings.Join(parts, " ")
}

func sortGameweek(rows []fpl.ManagerGW, by string) {
	switch by {
	case "gw":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].NetPoints > rows[j].NetPoints })
	case "bench":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Bench > rows[j].Bench })
	case "hits":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Hit > rows[j].Hit })
	case "gwrank":
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].GWRank == 0 {
				return false
			}
			if rows[j].GWRank == 0 {
				return true
			}
			return rows[i].GWRank < rows[j].GWRank
		})
	default:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].LeagueRank < rows[j].LeagueRank })
	}
}

func sortSeason(rows []fpl.ManagerSeason, by string) {
	switch by {
	case "bench":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].BenchTotal > rows[j].BenchTotal })
	case "hits":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].HitsTotal > rows[j].HitsTotal })
	default:
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].LeagueRank < rows[j].LeagueRank })
	}
}

func trunc(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func comma(n int) string {
	if n == 0 {
		return "—"
	}
	s := fmt.Sprintf("%d", n)
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
