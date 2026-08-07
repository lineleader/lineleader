package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lineleader/lineleader/internal/ledger"
)

const ledgerDateLayout = "2006-01-02"

// runLedger dispatches the `dvc ledger <sub>` commands.
func runLedger(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(ledgerUsage())
	}
	switch args[0] {
	case "show":
		return runLedgerShow(args[1:], out)
	case "contracts":
		return runLedgerContracts(args[1:], out)
	case "add":
		return runLedgerAdd(args[1:], out)
	case "edit":
		return runLedgerEdit(args[1:], out)
	case "delete":
		return runLedgerDelete(args[1:], out)
	case "distribute":
		return runLedgerDistribute(args[1:], out)
	default:
		return fmt.Errorf("unknown ledger command: %s\n\n%s", args[0], ledgerUsage())
	}
}

func ledgerUsage() string {
	return `dvc ledger — DVC points master ledger

Usage:
  dvc ledger show [--dsn DSN]
  dvc ledger contracts list [--dsn DSN]
  dvc ledger contracts add --name NAME [--number N] [--resort CODE] --points N --use-year-month MON [--dsn DSN]
  dvc ledger add --date YYYY-MM-DD --desc TEXT [--year N] [--kind allocation|usage|bonus|single_use|adjustment]
                 [--allotted N] [--used N] [--tag TEXT] [--dsn DSN]
  dvc ledger edit --id N [same flags as add] [--dsn DSN]
  dvc ledger delete --id N [--dsn DSN]
  dvc ledger distribute [--dsn DSN]

DSN defaults to the LEDGER_DSN environment variable when --dsn is omitted.`
}

// defaultLedgerDSN is the --dsn flag default: the LEDGER_DSN environment
// variable, or empty (openLedger then reports a clear error).
func defaultLedgerDSN() string {
	return os.Getenv("LEDGER_DSN")
}

// openLedger opens the store at the given Postgres DSN (from --dsn, added to
// fs) after parsing.
func openLedger(dsn string) (*ledger.Store, error) {
	if dsn == "" {
		return nil, errors.New("ledger: no database DSN provided (use --dsn or set LEDGER_DSN)")
	}
	s, err := ledger.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("ledger: opening %s: %w", dsn, err)
	}
	return s, nil
}

func runLedgerShow(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	s, err := openLedger(*dsn)
	if err != nil {
		return err
	}
	defer s.Close()

	entries, err := s.ListEntries()
	if err != nil {
		return fmt.Errorf("ledger show: %w", err)
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tYEAR\tDATE\tDESC\tALLOTTED\tUSED\tTOTAL\tTAG")
	for _, e := range entries {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%d\t%s\n",
			e.ID, e.UseYear, e.Date.Format(ledgerDateLayout), e.Desc,
			pointsCol(e.Allotted), pointsCol(e.Used), e.RunningBalance, e.Tag)
	}
	tw.Flush()

	summaries, err := s.UseYearSummaries()
	if err != nil {
		return fmt.Errorf("ledger show: %w", err)
	}
	if len(summaries) > 0 {
		fmt.Fprintln(out, "\nPer use year:")
		st := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(st, "YEAR\tALLOTTED\tUSED\tNET\t")
		for _, y := range summaries {
			flagStr := ""
			if y.OverBorrowed {
				flagStr = "OVER-BORROWED"
			}
			fmt.Fprintf(st, "%d\t%d\t%d\t%d\t%s\n", y.UseYear, y.Allotted, y.Used, y.Net, flagStr)
		}
		st.Flush()
	}
	return nil
}

func runLedgerContracts(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(ledgerUsage())
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("ledger contracts list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		s, err := openLedger(*dsn)
		if err != nil {
			return err
		}
		defer s.Close()
		contracts, err := s.ListContracts()
		if err != nil {
			return fmt.Errorf("ledger contracts: %w", err)
		}
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tNUMBER\tRESORT\tPOINTS\tUSE-YEAR")
		for _, c := range contracts {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n",
				c.ID, c.Name, c.Number, c.HomeResort, c.AnnualPoints, c.UseYearMonth.String())
		}
		tw.Flush()
		return nil
	case "add":
		fs := flag.NewFlagSet("ledger contracts add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
		name := fs.String("name", "", "contract name")
		number := fs.String("number", "", "DVC contract/membership number")
		resort := fs.String("resort", "", "home resort code")
		points := fs.Int("points", 0, "annual points")
		month := fs.String("use-year-month", "", "use year start month (e.g. Apr or 4)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" || *points == 0 || *month == "" {
			return errors.New("ledger contracts add: --name, --points, and --use-year-month are required")
		}
		m, err := parseMonth(*month)
		if err != nil {
			return fmt.Errorf("ledger contracts add: %w", err)
		}
		s, err := openLedger(*dsn)
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.AddContract(ledger.Contract{
			Name: *name, Number: *number, HomeResort: *resort,
			AnnualPoints: *points, UseYearMonth: m,
		})
		if err != nil {
			return fmt.Errorf("ledger contracts add: %w", err)
		}
		fmt.Fprintf(out, "added contract %d (%s, %d pts, use year %s)\n", id, *name, *points, m)
		return nil
	default:
		return errors.New(ledgerUsage())
	}
}

func runLedgerAdd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
	f := entryFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *f.date == "" || *f.desc == "" {
		return errors.New("ledger add: --date and --desc are required")
	}
	e, err := f.toEntry()
	if err != nil {
		return fmt.Errorf("ledger add: %w", err)
	}
	s, err := openLedger(*dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	id, err := s.AddEntry(e)
	if err != nil {
		return fmt.Errorf("ledger add: %w", err)
	}
	fmt.Fprintf(out, "added entry %d\n", id)
	return nil
}

func runLedgerEdit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
	id := fs.Int64("id", 0, "entry id to edit")
	f := entryFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *id == 0 {
		return errors.New("ledger edit: --id is required")
	}
	if *f.date == "" || *f.desc == "" {
		return errors.New("ledger edit: --date and --desc are required")
	}
	e, err := f.toEntry()
	if err != nil {
		return fmt.Errorf("ledger edit: %w", err)
	}
	e.ID = *id
	s, err := openLedger(*dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.UpdateEntry(e); err != nil {
		return fmt.Errorf("ledger edit: %w", err)
	}
	fmt.Fprintf(out, "updated entry %d\n", *id)
	return nil
}

func runLedgerDelete(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
	id := fs.Int64("id", 0, "entry id to delete")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return errors.New("ledger delete: --id is required")
	}
	s, err := openLedger(*dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.DeleteEntry(*id); err != nil {
		return fmt.Errorf("ledger delete: %w", err)
	}
	fmt.Fprintf(out, "deleted entry %d\n", *id)
	return nil
}

func runLedgerDistribute(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger distribute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dsn := fs.String("dsn", defaultLedgerDSN(), "ledger Postgres DSN (or set LEDGER_DSN)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openLedger(*dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	created, err := s.DistributeNextYear()
	if err != nil {
		return fmt.Errorf("ledger distribute: %w", err)
	}
	if len(created) == 0 {
		fmt.Fprintln(out, "nothing to distribute (already up to date)")
		return nil
	}
	for _, e := range created {
		fmt.Fprintf(out, "distributed %d pts to use year %d (%s)\n", e.Allotted, e.UseYear, e.Desc)
	}
	return nil
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
