package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lineleader/lineleader/internal/dvc"
)

const defaultDataDir = "data/point-charts"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	switch os.Args[1] {
	case "import":
		runImport(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "tui":
		runTUI(os.Args[2:])
	case "list":
		runList(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, `dvc — Disney Vacation Club stay search

Usage:
  dvc import [--data-dir PATH] [--dir SCAN_DIR] [pdf-file...]
  dvc search --from DATE --to DATE --budget N [--min-nights N] [--data-dir PATH]
  dvc tui    [--data-dir PATH]
  dvc list   [--data-dir PATH]`)
}

// runImport parses one or more PDF point chart files and saves them as JSON.
func runImport(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "directory for JSON chart files")
	scanDir := fs.String("dir", "", "walk this directory and import all PDFs found")
	fs.Parse(args)

	files := fs.Args()
	if *scanDir != "" {
		found, err := dvc.CollectPDFs(*scanDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "import: scanning %s: %v\n", *scanDir, err)
			os.Exit(1)
		}
		files = append(files, found...)
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "import: no PDF files specified")
		os.Exit(1)
	}

	for _, path := range files {
		chart, err := dvc.ParsePDF(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
			continue
		}
		if err := dvc.SaveChart(*dataDir, chart); err != nil {
			fmt.Fprintf(os.Stderr, "saving %s: %v\n", path, err)
			os.Exit(1)
		}
		fmt.Printf("imported %s (%s %d, %d seasons, %d columns)\n",
			path, chart.ResortCode, chart.Year, len(chart.Seasons), len(chart.Columns))
	}
}

// runSearch loads charts and prints stays within budget.
func runSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "directory with JSON chart files")
	from := fs.String("from", "", "window start date (YYYY-MM-DD)")
	to := fs.String("to", "", "window end date (YYYY-MM-DD)")
	budget := fs.Int("budget", 0, "maximum total points")
	minNights := fs.Int("min-nights", 1, "minimum stay length")
	fs.Parse(args)

	if *from == "" || *to == "" || *budget == 0 {
		fmt.Fprintln(os.Stderr, "search: --from, --to, and --budget are required")
		os.Exit(1)
	}

	windowStart, err := parseDate(*from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --from date: %v\n", err)
		os.Exit(1)
	}
	windowEnd, err := parseDate(*to)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid --to date: %v\n", err)
		os.Exit(1)
	}

	charts, err := dvc.LoadAll(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading charts: %v\n", err)
		os.Exit(1)
	}
	if len(charts) == 0 {
		fmt.Fprintf(os.Stderr, "no charts found in %s — run 'dvc import' first\n", *dataDir)
		os.Exit(1)
	}

	params := dvc.SearchParams{
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
		Budget:      *budget,
		MinNights:   *minNights,
	}

	results := dvc.Search(charts, params)
	dvc.PrintTable(os.Stdout, results, params)
}

// runList shows what resort/year data is available.
func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "directory with JSON chart files")
	fs.Parse(args)

	charts, err := dvc.LoadAll(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading charts: %v\n", err)
		os.Exit(1)
	}
	if len(charts) == 0 {
		fmt.Printf("no charts found in %s\n", *dataDir)
		return
	}
	for _, c := range charts {
		fmt.Printf("%s %d  (%d seasons, %d columns)\n",
			c.ResortCode, c.Year, len(c.Seasons), len(c.Columns))
	}
}

// runTUI launches the interactive full-screen search UI.
func runTUI(args []string) {
	fs := flag.NewFlagSet("tui", flag.ExitOnError)
	dataDir := fs.String("data-dir", defaultDataDir, "directory with JSON chart files")
	fs.Parse(args)

	charts, err := dvc.LoadAll(*dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading charts: %v\n", err)
		os.Exit(1)
	}
	if len(charts) == 0 {
		fmt.Fprintf(os.Stderr, "no charts found in %s — run 'dvc import' first\n", *dataDir)
		os.Exit(1)
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	m := dvc.NewTUIModel(charts)
	m = m.WithDefaults(
		today.Format("2006-01-02"),
		today.AddDate(0, 0, 14).Format("2006-01-02"),
		"100",
		"1",
	)

	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui: %v\n", err)
		os.Exit(1)
	}
}

// parseDate parses a date string in YYYY-MM-DD or M/D/YYYY format.
func parseDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "1/2/2006", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse %q — use YYYY-MM-DD or M/D/YYYY", s)
}
