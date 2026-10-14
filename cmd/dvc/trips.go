package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lineleader/lineleader/internal/ledgerclient"
)

// runTrips dispatches the `dvc trips <sub>` commands. Like `dvc ledger`, it
// is a thin HTTP client over the server's JSON API.
func runTrips(args []string, out io.Writer) error {
	if len(args) < 1 {
		return errors.New(tripsUsage())
	}
	switch args[0] {
	case "import":
		return runTripsImport(args[1:], out)
	default:
		return fmt.Errorf("unknown trips command: %s\n\n%s", args[0], tripsUsage())
	}
}

func tripsUsage() string {
	return `dvc trips — trips on the lineleader server

Usage:
  dvc trips import --file trips.json [--server URL] [--token TOKEN]

trips.json is a JSON array of already-booked trips, each linked to EXISTING
ledger usage entries (no allocator runs, no entries are created):
  [{"name": "...", "start_date": "YYYY-MM-DD", "end_date": "YYYY-MM-DD", "min_nights": 1,
    "stays": [{"resort": "...", "room_type": "...", "view": "...",
               "check_in": "YYYY-MM-DD", "check_out": "YYYY-MM-DD",
               "points": N, "entry_ids": [ID, ...]}]}]
start_date/end_date/min_nights are optional.`
}

func runTripsImport(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("trips import", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "JSON file holding an array of trips to import")
	server, token := addServerFlags(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" {
		return errors.New("trips import: --file is required\n\n" + tripsUsage())
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("trips import: %w", err)
	}
	var trips []ledgerclient.ImportTripInput
	if err := json.Unmarshal(data, &trips); err != nil {
		return fmt.Errorf("trips import: parsing %s (want a JSON array of trips): %w", *file, err)
	}
	cl, err := newLedgerClient(*server, *token)
	if err != nil {
		return err
	}
	for _, t := range trips {
		id, err := cl.ImportTrip(context.Background(), t)
		if err != nil {
			return fmt.Errorf("trips import: trip %q: %w", t.Name, err)
		}
		fmt.Fprintf(out, "imported trip %d (%s)\n", id, t.Name)
	}
	return nil
}
