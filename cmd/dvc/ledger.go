package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

const ledgerDateLayout = "2006-01-02"

// runLedger dispatches the `dvc ledger <sub>` commands.
func runLedger(args []string) {
	if len(args) < 1 {
		ledgerUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "show":
		runLedgerShow(args[1:])
	case "contracts":
		runLedgerContracts(args[1:])
	case "add":
		runLedgerAdd(args[1:])
	case "edit":
		runLedgerEdit(args[1:])
	case "delete":
		runLedgerDelete(args[1:])
	case "distribute":
		runLedgerDistribute(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown ledger command: %s\n\n", args[0])
		ledgerUsage()
		os.Exit(1)
	}
}

func ledgerUsage() {
	fmt.Fprintln(os.Stderr, `dvc ledger — DVC points master ledger

Usage:
  dvc ledger show [--db PATH]
  dvc ledger contracts list [--db PATH]
  dvc ledger contracts add --name NAME [--number N] [--resort CODE] --points N --use-year-month MON [--db PATH]
  dvc ledger add --date YYYY-MM-DD --desc TEXT [--year N] [--kind allocation|usage|bonus|single_use|adjustment]
                 [--allotted N] [--used N] [--tag TEXT] [--db PATH]
  dvc ledger edit --id N [same flags as add] [--db PATH]
  dvc ledger delete --id N [--db PATH]
  dvc ledger distribute [--db PATH]`)
}

// openLedger opens the store at the --db path (added to fs) after parsing.
func openLedger(dbPath string) *ledger.Store {
	s, err := ledger.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger: opening %s: %v\n", dbPath, err)
		os.Exit(1)
	}
	return s
}

func runLedgerShow(args []string) {
	fs := flag.NewFlagSet("ledger show", flag.ExitOnError)
	db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
	fs.Parse(args)

	s := openLedger(*db)
	defer s.Close()

	entries, err := s.ListEntries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger show: %v\n", err)
		os.Exit(1)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tYEAR\tDATE\tDESC\tALLOTTED\tUSED\tTOTAL\tTAG")
	for _, e := range entries {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%d\t%s\n",
			e.ID, e.UseYear, e.Date.Format(ledgerDateLayout), e.Desc,
			pointsCol(e.Allotted), pointsCol(e.Used), e.RunningBalance, e.Tag)
	}
	tw.Flush()

	summaries, err := s.UseYearSummaries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger show: %v\n", err)
		os.Exit(1)
	}
	if len(summaries) > 0 {
		fmt.Println("\nPer use year:")
		st := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(st, "YEAR\tALLOTTED\tUSED\tNET\t")
		for _, y := range summaries {
			flag := ""
			if y.OverBorrowed {
				flag = "OVER-BORROWED"
			}
			fmt.Fprintf(st, "%d\t%d\t%d\t%d\t%s\n", y.UseYear, y.Allotted, y.Used, y.Net, flag)
		}
		st.Flush()
	}
}

func runLedgerContracts(args []string) {
	if len(args) < 1 {
		ledgerUsage()
		os.Exit(1)
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("ledger contracts list", flag.ExitOnError)
		db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
		fs.Parse(args[1:])
		s := openLedger(*db)
		defer s.Close()
		contracts, err := s.ListContracts()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger contracts: %v\n", err)
			os.Exit(1)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tNUMBER\tRESORT\tPOINTS\tUSE-YEAR")
		for _, c := range contracts {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n",
				c.ID, c.Name, c.Number, c.HomeResort, c.AnnualPoints, c.UseYearMonth.String())
		}
		tw.Flush()
	case "add":
		fs := flag.NewFlagSet("ledger contracts add", flag.ExitOnError)
		db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
		name := fs.String("name", "", "contract name")
		number := fs.String("number", "", "DVC contract/membership number")
		resort := fs.String("resort", "", "home resort code")
		points := fs.Int("points", 0, "annual points")
		month := fs.String("use-year-month", "", "use year start month (e.g. Apr or 4)")
		fs.Parse(args[1:])
		if *name == "" || *points == 0 || *month == "" {
			fmt.Fprintln(os.Stderr, "ledger contracts add: --name, --points, and --use-year-month are required")
			os.Exit(1)
		}
		m, err := parseMonth(*month)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger contracts add: %v\n", err)
			os.Exit(1)
		}
		s := openLedger(*db)
		defer s.Close()
		id, err := s.AddContract(ledger.Contract{
			Name: *name, Number: *number, HomeResort: *resort,
			AnnualPoints: *points, UseYearMonth: m,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "ledger contracts add: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("added contract %d (%s, %d pts, use year %s)\n", id, *name, *points, m)
	default:
		ledgerUsage()
		os.Exit(1)
	}
}

func runLedgerAdd(args []string) {
	fs := flag.NewFlagSet("ledger add", flag.ExitOnError)
	db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
	f := entryFlags(fs)
	fs.Parse(args)

	if *f.date == "" || *f.desc == "" {
		fmt.Fprintln(os.Stderr, "ledger add: --date and --desc are required")
		os.Exit(1)
	}
	e, err := f.toEntry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger add: %v\n", err)
		os.Exit(1)
	}
	s := openLedger(*db)
	defer s.Close()
	id, err := s.AddEntry(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger add: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("added entry %d\n", id)
}

func runLedgerEdit(args []string) {
	fs := flag.NewFlagSet("ledger edit", flag.ExitOnError)
	db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
	id := fs.Int64("id", 0, "entry id to edit")
	f := entryFlags(fs)
	fs.Parse(args)

	if *id == 0 {
		fmt.Fprintln(os.Stderr, "ledger edit: --id is required")
		os.Exit(1)
	}
	if *f.date == "" || *f.desc == "" {
		fmt.Fprintln(os.Stderr, "ledger edit: --date and --desc are required")
		os.Exit(1)
	}
	e, err := f.toEntry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger edit: %v\n", err)
		os.Exit(1)
	}
	e.ID = *id
	s := openLedger(*db)
	defer s.Close()
	if err := s.UpdateEntry(e); err != nil {
		fmt.Fprintf(os.Stderr, "ledger edit: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("updated entry %d\n", *id)
}

func runLedgerDelete(args []string) {
	fs := flag.NewFlagSet("ledger delete", flag.ExitOnError)
	db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
	id := fs.Int64("id", 0, "entry id to delete")
	fs.Parse(args)
	if *id == 0 {
		fmt.Fprintln(os.Stderr, "ledger delete: --id is required")
		os.Exit(1)
	}
	s := openLedger(*db)
	defer s.Close()
	if err := s.DeleteEntry(*id); err != nil {
		fmt.Fprintf(os.Stderr, "ledger delete: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("deleted entry %d\n", *id)
}

func runLedgerDistribute(args []string) {
	fs := flag.NewFlagSet("ledger distribute", flag.ExitOnError)
	db := fs.String("db", ledger.DefaultLedgerPath(), "ledger database file")
	fs.Parse(args)
	s := openLedger(*db)
	defer s.Close()
	created, err := s.DistributeNextYear()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ledger distribute: %v\n", err)
		os.Exit(1)
	}
	if len(created) == 0 {
		fmt.Println("nothing to distribute (already up to date)")
		return
	}
	for _, e := range created {
		fmt.Printf("distributed %d pts to use year %d (%s)\n", e.Allotted, e.UseYear, e.Desc)
	}
}

// entryFields holds the shared add/edit flag pointers.
type entryFields struct {
	year     *int
	date     *string
	desc     *string
	kind     *string
	allotted *int
	used     *int
	tag      *string
	contract *int64
}

func entryFlags(fs *flag.FlagSet) entryFields {
	return entryFields{
		year:     fs.Int("year", 0, "use year (defaults to the year of --date)"),
		date:     fs.String("date", "", "transaction date (YYYY-MM-DD)"),
		desc:     fs.String("desc", "", "description"),
		kind:     fs.String("kind", ledger.KindUsage, "allocation|usage|bonus|single_use|adjustment"),
		allotted: fs.Int("allotted", 0, "points added"),
		used:     fs.Int("used", 0, "points used"),
		tag:      fs.String("tag", "", "annotation (Bank|Borrow|...)"),
		contract: fs.Int64("contract", 0, "link an allocation row to a contract id (enables distribute)"),
	}
}

func (f entryFields) toEntry() (ledger.Entry, error) {
	d, err := time.Parse(ledgerDateLayout, *f.date)
	if err != nil {
		return ledger.Entry{}, fmt.Errorf("invalid --date: %w", err)
	}
	year := *f.year
	if year == 0 {
		year = d.Year() // sensible default; override with --year for banked/borrowed rows
	}
	e := ledger.Entry{
		UseYear:  year,
		Date:     d,
		Desc:     *f.desc,
		Kind:     *f.kind,
		Allotted: *f.allotted,
		Used:     *f.used,
		Tag:      *f.tag,
	}
	if *f.contract != 0 {
		id := *f.contract
		e.ContractID = &id
	}
	return e, nil
}

// pointsCol renders 0 as a blank so the grid matches the spreadsheet's empty cells.
func pointsCol(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}

// parseMonth accepts a month number (1-12) or an English month name/abbreviation.
func parseMonth(s string) (time.Month, error) {
	s = strings.TrimSpace(s)
	for m := time.January; m <= time.December; m++ {
		if strings.EqualFold(s, m.String()) || strings.EqualFold(s, m.String()[:3]) {
			return m, nil
		}
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && n >= 1 && n <= 12 {
		return time.Month(n), nil
	}
	return 0, fmt.Errorf("invalid month %q (use Apr, April, or 4)", s)
}
