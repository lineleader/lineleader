package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/lineleader/lineleader/internal/ledgerclient"
)

// runLedger dispatches the `dvc ledger <sub>` commands. The CLI is a thin
// HTTP client over the server's /api/v1/ledger API (see
// docs/pitches/hosted-lineleader.md, "4. CLI becomes a thin client") — it
// opens no database and links no database driver.
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
  dvc ledger show [--server URL] [--token TOKEN]
  dvc ledger contracts list [--server URL] [--token TOKEN]
  dvc ledger contracts add --name NAME [--number N] [--resort CODE] --points N --use-year-month MON [--server URL] [--token TOKEN]
  dvc ledger add --date YYYY-MM-DD --desc TEXT [--year N] [--kind allocation|usage|bonus|single_use|adjustment]
                 [--allotted N] [--used N] [--tag TEXT] [--server URL] [--token TOKEN]
  dvc ledger edit --id N [any of the add flags — only the ones given are changed] [--server URL] [--token TOKEN]
  dvc ledger delete --id N [--server URL] [--token TOKEN]
  dvc ledger distribute [--server URL] [--token TOKEN]

Server URL and token are resolved, in priority order, from --server/--token,
the LINELEADER_SERVER/LINELEADER_TOKEN environment variables, and finally
~/.config/lineleader/client.json ({"server_url": "...", "token": "..."}).`
}

// addServerFlags registers the --server/--token flags shared by every ledger
// subcommand.
func addServerFlags(fs *flag.FlagSet) (server, token *string) {
	server = fs.String("server", "", "lineleader server URL (or set LINELEADER_SERVER)")
	token = fs.String("token", "", "auth token (or set LINELEADER_TOKEN)")
	return server, token
}

// newLedgerClient resolves the server URL/token (flags > env > config file —
// see ledgerclient.ResolveConfig) and builds a client for it.
func newLedgerClient(server, token string) (*ledgerclient.Client, error) {
	cfg, err := ledgerclient.ResolveConfig(server, token)
	if err != nil {
		return nil, err
	}
	return ledgerclient.New(cfg.ServerURL, cfg.Token), nil
}

func runLedgerShow(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger show", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server, token := addServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	ctx := context.Background()

	entries, err := cl.ListEntries(ctx)
	if err != nil {
		return fmt.Errorf("ledger show: %w", err)
	}

	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tYEAR\tDATE\tDESC\tALLOTTED\tUSED\tTOTAL\tTAG")
	for _, e := range entries {
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%d\t%s\n",
			e.ID, e.UseYear, e.Date, e.Desc,
			pointsCol(e.Allotted), pointsCol(e.Used), e.RunningBalance, e.Tag)
	}
	tw.Flush()

	summaries, err := cl.Summaries(ctx)
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
		server, token := addServerFlags(fs)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cl, err := newLedgerClient(*server, *token)
		if err != nil {
			return err
		}
		contracts, err := cl.ListContracts(context.Background())
		if err != nil {
			return fmt.Errorf("ledger contracts: %w", err)
		}
		tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tNUMBER\tRESORT\tPOINTS\tUSE-YEAR")
		for _, c := range contracts {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n",
				c.ID, c.Name, c.Number, c.HomeResort, c.AnnualPoints, c.UseYearMonth)
		}
		tw.Flush()
		return nil
	case "add":
		fs := flag.NewFlagSet("ledger contracts add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		server, token := addServerFlags(fs)
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
		cl, err := newLedgerClient(*server, *token)
		if err != nil {
			return err
		}
		c, err := cl.AddContract(context.Background(), ledgerclient.ContractInput{
			Name: *name, Number: *number, HomeResort: *resort,
			AnnualPoints: *points, UseYearMonth: *month,
		})
		if err != nil {
			return fmt.Errorf("ledger contracts add: %w", err)
		}
		fmt.Fprintf(out, "added contract %d (%s, %d pts, use year %s)\n", c.ID, c.Name, c.AnnualPoints, c.UseYearMonth)
		return nil
	default:
		return errors.New(ledgerUsage())
	}
}

func runLedgerAdd(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server, token := addServerFlags(fs)
	f := entryFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *f.date == "" || *f.desc == "" {
		return errors.New("ledger add: --date and --desc are required")
	}
	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	e, err := cl.AddEntry(context.Background(), f.toEntryInput())
	if err != nil {
		return fmt.Errorf("ledger add: %w", err)
	}
	fmt.Fprintf(out, "added entry %d\n", e.ID)
	return nil
}

// runLedgerEdit does a read-modify-write over the entry identified by --id:
// it fetches the current entries (the server's PUT doesn't report a 404 for
// an unknown id — see internal/web/api_handlers.go's updateEntry note), errors
// clearly if --id isn't found, then applies only the flags the caller
// actually passed (via fs.Visit) on top of the existing values before PUTting
// the result. This makes `dvc ledger edit --id 3 --tag Bank` a true partial
// update instead of silently resetting every other field to its flag default.
func runLedgerEdit(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger edit", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server, token := addServerFlags(fs)
	id := fs.Int64("id", 0, "entry id to edit")
	f := entryFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return errors.New("ledger edit: --id is required")
	}

	set := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { set[fl.Name] = true })

	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	ctx := context.Background()

	entries, err := cl.ListEntries(ctx)
	if err != nil {
		return fmt.Errorf("ledger edit: %w", err)
	}
	var existing *ledgerclient.Entry
	for i := range entries {
		if entries[i].ID == *id {
			existing = &entries[i]
			break
		}
	}
	if existing == nil {
		return fmt.Errorf("ledger edit: entry %d not found", *id)
	}

	in := ledgerclient.EntryInput{
		UseYear:    existing.UseYear,
		Date:       existing.Date,
		Desc:       existing.Desc,
		Kind:       existing.Kind,
		Allotted:   existing.Allotted,
		Used:       existing.Used,
		Tag:        existing.Tag,
		ContractID: existing.ContractID,
	}
	if set["year"] {
		in.UseYear = *f.year
	}
	if set["date"] {
		in.Date = *f.date
	}
	if set["desc"] {
		in.Desc = *f.desc
	}
	if set["kind"] {
		in.Kind = *f.kind
	}
	if set["allotted"] {
		in.Allotted = *f.allotted
	}
	if set["used"] {
		in.Used = *f.used
	}
	if set["tag"] {
		in.Tag = *f.tag
	}
	if set["contract"] {
		v := *f.contract
		in.ContractID = &v
	}

	updated, err := cl.UpdateEntry(ctx, *id, in)
	if err != nil {
		return fmt.Errorf("ledger edit: %w", err)
	}
	fmt.Fprintf(out, "updated entry %d\n", updated.ID)
	return nil
}

func runLedgerDelete(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger delete", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server, token := addServerFlags(fs)
	id := fs.Int64("id", 0, "entry id to delete")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == 0 {
		return errors.New("ledger delete: --id is required")
	}
	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	if err := cl.DeleteEntry(context.Background(), *id); err != nil {
		return fmt.Errorf("ledger delete: %w", err)
	}
	fmt.Fprintf(out, "deleted entry %d\n", *id)
	return nil
}

func runLedgerDistribute(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("ledger distribute", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server, token := addServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	created, err := cl.Distribute(context.Background())
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
		kind:     fs.String("kind", ledgerclient.KindUsage, "allocation|usage|bonus|single_use|adjustment"),
		allotted: fs.Int("allotted", 0, "points added"),
		used:     fs.Int("used", 0, "points used"),
		tag:      fs.String("tag", "", "annotation (Bank|Borrow|...)"),
		contract: fs.Int64("contract", 0, "link an allocation row to a contract id (enables distribute)"),
	}
}

// toEntryInput builds the request body for add. Date/kind/use-year validation
// and defaulting (e.g. use_year defaulting to the date's year when --year is
// omitted) happens server-side — see entryRequestDTO.toEntry in
// internal/web/api_handlers.go.
func (f entryFields) toEntryInput() ledgerclient.EntryInput {
	in := ledgerclient.EntryInput{
		UseYear:  *f.year,
		Date:     *f.date,
		Desc:     *f.desc,
		Kind:     *f.kind,
		Allotted: *f.allotted,
		Used:     *f.used,
		Tag:      *f.tag,
	}
	if *f.contract != 0 {
		id := *f.contract
		in.ContractID = &id
	}
	return in
}

// pointsCol renders 0 as a blank so the grid matches the spreadsheet's empty cells.
func pointsCol(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d", n)
}
