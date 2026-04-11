# DVC Stay Search CLI — Implementation Plan

## Context

Build a CLI tool for `github.com/lineleader/lineleader` that lets a user enter a travel window (date range) and a points budget, then finds all possible DVC stays within that window that fit the budget. "Any stay" means all valid (check-in, check-out) pairs across all resorts and room types, not just a single fixed stay.

Data source: PDF point charts at `~/Documents/DVC/point-charts/`. Currently 2 PDFs exist (VGF 2026 and 2027); more will be added. PDFs use a table format with 10 columns of nightly point costs organized by travel season.

---

## Architecture: Two-Phase (import → search)

1. **`dvc import <pdf>`** — parse PDF → JSON file in `data/point-charts/`
2. **`dvc search --from DATE --to DATE --budget N`** — query JSON files

Both are subcommands of a single binary: `bin/dvc` from `cmd/dvc/main.go`.

Rationale: Pre-processing to JSON keeps search fast, decouples PDF parsing from search, and lets data files be committed to git.

---

## File Structure

```
cmd/dvc/main.go                    CLI entry point (import + search subcommands)
internal/dvc/
  types.go                         All data model types
  parser.go                        pdftotext -layout → ResortChart
  store.go                         JSON load/save
  search.go                        Points calculation + stay enumeration
  table.go                         text/tabwriter output
data/point-charts/                 Generated JSON files (git-committable)
  2026_VGF.json
  2027_VGF.json
```

The existing `cmd/server/main.go` is untouched.

---

## Data Model — `internal/dvc/types.go`

```go
type ResortChart struct {
    ResortName string   `json:"resort_name"` // "The Villas at Disney's Grand Floridian..."
    ResortCode string   `json:"resort_code"` // "VGF"
    Year       int      `json:"year"`
    Columns    []Column `json:"columns"`     // ordered column definitions
    Seasons    []Season `json:"seasons"`
}

type Column struct {
    RoomType string `json:"room_type"` // "RESORT STUDIO", "DELUXE STUDIO", etc.
    View     string `json:"view"`      // "R", "P", "TP", "" (empty = no view distinction)
    Sleeps   int    `json:"sleeps"`
}

type Season struct {
    Periods []DateRange `json:"periods"`    // 1–3 date ranges sharing this rate tier
    SunThu  []int       `json:"sun_thu"`    // per-column nightly rates Sun–Thu
    FriSat  []int       `json:"fri_sat"`    // per-column nightly rates Fri–Sat
    // Weekly omitted: redundant (5×SunThu + 2×FriSat)
}

type DateRange struct {
    Start string `json:"start"` // "2026-01-01" (YYYY-MM-DD)
    End   string `json:"end"`
}

// Search types
type SearchParams struct {
    WindowStart time.Time
    WindowEnd   time.Time
    Budget      int
    MinNights   int // default 1
}

type StayResult struct {
    Resort   string
    RoomType string
    View     string
    CheckIn  time.Time
    CheckOut time.Time
    Nights   int
    Points   int
}
```

---

## PDF Parsing — `internal/dvc/parser.go`

Use `pdftotext -layout` (confirmed to work well on these PDFs). The layout output keeps each table row on a single line with consistent spacing.

### Confirmed PDF structure (from both VGF 2026 and 2027)

```
The Villas at Disney's Grand Floridian Resort & Spa
AT WALT DISNEY WORLD® RESORT

2026 VACATION POINTS PER NIGHT
     NIGHTS     RESORT STUDIO   DELUXE STUDIO   ONE-BEDROOM   TWO-BEDROOM   THREE-BEDROOM
                (Sleeps up to 5) (Sleeps up to 5)    VILLA          VILLA      GRAND VILLA
TP - Theme Park View         R         P        TP        R         P         R         P         R         P              P

TRAVEL PERIODS
               SUN—THU   16   19   24   16   19   31   39   44   54   111
               FRI—SAT   20   24   27   20   24   41   48   55   65   131
Sept 1 - Sept 30
                WEEKLY   120  143  174  120  143  237  291  330  400  817

               SUN—THU   17   21   25   17   21   36   43   49   59   118
Jan 1 - Jan 31  FRI—SAT  20   24   29   20   24   44   51   58   68   138
May 1 - May 14   WEEKLY  125  153  183  125  153  268  317  361  431  866
...
```

Key observations:
- Date ranges appear on **standalone lines** OR on the **same line as FRI—SAT or WEEKLY** (to the left of the keyword)
- The view codes line (last line of the header) gives the ordered column list for the whole file
- 10 columns for VGF: R P TP R P R P R P P

### Parsing Algorithm

**Phase 1 — Column extraction:**
- Find the line containing `SUN—THU` for the FIRST time. Everything above is the header.
- In the header, find the line containing view codes (`R`, `P`, `TP` etc.) without room-type words. This is the column-definition line.
- Parse room type names (RESORT STUDIO, DELUXE STUDIO, etc.) from the lines above.
- Match view codes to room types using horizontal character position: each room type name is centered above its column group. The view code at x-position X belongs to the room type whose header spans over X.
- Store the resulting `[]Column` in the chart.

**Phase 2 — Season extraction (state machine):**

State: accumulate date ranges and rate rows until a complete season is assembled.

```
pending.dates   []DateRange
pending.sunThu  []int
pending.friSat  []int
pending.weekly  []int

for each line after "TRAVEL PERIODS":
    if line contains date pattern "Month D - Month D":
        parse date ranges from line; add to pending.dates
    if line contains "SUN—THU":
        flush previous pending season (if complete)
        parse integers from line → pending.sunThu
    if line contains "FRI—SAT":
        parse integers from line → pending.friSat
        also check left side for date ranges → pending.dates
    if line contains "WEEKLY":
        skip rate values (redundant: 5×SunThu + 2×FriSat)
        also check left side for date ranges → pending.dates
        flush pending season (WEEKLY = end of a season block)

flush final pending season
```

A season is "complete" when sunThu, friSat, weekly, and ≥1 date range are all present.

**Key function signatures:**
```go
func ParsePDF(pdfPath string) (*ResortChart, error)
func extractText(pdfPath string) (string, error)   // runs pdftotext -layout
func parseColumns(headerLines []string) ([]Column, error)
func parseSeasons(dataLines []string, year int) ([]Season, error)
func parseDateRange(s string, year int) (DateRange, error)
func parseInts(s string) []int  // extract all integers from a string; used for SUN-THU and FRI-SAT rows only
func extractResortCode(filename string) string  // "VGF-2026.pdf" → "VGF"
```

---

## Store — `internal/dvc/store.go`

```go
func SaveChart(dataDir string, chart *ResortChart) error
// Writes to dataDir/<year>_<ResortCode>.json

func LoadAll(dataDir string) ([]*ResortChart, error)
// Reads all *.json files from dataDir
```

Uses `encoding/json`. No external dependencies.

---

## Search Algorithm — `internal/dvc/search.go`

```go
// PointsForNight returns per-night cost for a given column on a given date.
// Looks up the season covering date, then returns FriSat or SunThu rate.
func PointsForNight(chart *ResortChart, date time.Time, colIdx int) (int, error)

// isWeekend returns true for Friday and Saturday nights (DVC FRI-SAT rate applies).
func isWeekend(d time.Time) bool

// Search finds all (checkIn, checkOut) pairs within the window
// where total points ≤ budget, across all charts and all columns.
func Search(charts []*ResortChart, params SearchParams) []StayResult
```

**Search loop:**

```
for each chart:
  for each column col in chart.Columns:
    for checkIn = WindowStart; checkIn < WindowEnd; checkIn += 1 day:
      total = 0
      for checkOut = checkIn+1; checkOut <= WindowEnd; checkOut += 1 day:
        night = checkOut minus 1 day
        pts, err = PointsForNight(chart, night, col)
        if err: break  // date not covered by any season; stop extending
        total += pts
        if total > budget: break  // over budget; no point going longer
        nights = checkOut - checkIn
        if nights >= minNights:
          append StayResult{...}
```

Cross-year stays (e.g. Dec–Jan): `PointsForNight` receives the full `[]*ResortChart` slice and selects by matching `chart.Year == date.Year()` AND `chart.ResortCode == targetCode`.

Sort results ascending by Points, then CheckIn.

---

## Output — `internal/dvc/table.go`

Uses `text/tabwriter`. Column order:

```
RESORT  ROOM TYPE  VIEW  CHECK-IN  CHECK-OUT  NIGHTS  POINTS
---------------------------------------------------------------------
Grand Floridian Villas  Resort Studio  R  2026-03-15  2026-03-17  2  34
...
42 results  |  Budget: 200 pts  |  Window: 2026-03-15 – 2026-03-30
```

---

## CLI — `cmd/dvc/main.go`

```
dvc import [--data-dir PATH] <pdf-file> [pdf-files...]
dvc search --from DATE --to DATE --budget N [--min-nights N] [--data-dir PATH]
dvc list   [--data-dir PATH]  (shows loaded resort+year combos)
```

Date formats accepted: `2026-03-15` (ISO) and `3/15/2026` (US).

Default `--data-dir`: `data/point-charts` (relative to CWD).

---

## Makefile Additions

```makefile
.PHONY: dvc
dvc:
	go build -o bin/dvc ./cmd/dvc

.PHONY: import
import: dvc
	./bin/dvc import ~/Documents/DVC/point-charts/2026/VGF-2026.pdf
	./bin/dvc import ~/Documents/DVC/point-charts/2027/2027_VGF.pdf
```

---

## Implementation Order

1. **`internal/dvc/types.go`** — all types; no logic
2. **`internal/dvc/parser.go`** — PDF → ResortChart; test against both VGF PDFs
3. **`internal/dvc/store.go`** — SaveChart / LoadAll
4. **`internal/dvc/search.go`** — PointsForNight, isWeekend, Search
5. **`internal/dvc/table.go`** — PrintTable
6. **`cmd/dvc/main.go`** — wire everything together

---

## Verification

1. `make import` — produces `data/point-charts/2026_VGF.json` and `2027_VGF.json`
2. Inspect JSON: verify 7 seasons, 10 columns, correct point values for known dates
3. `bin/dvc search --from 2026-01-01 --to 2026-01-10 --budget 50` — should return Resort Studio R (16 pts/night Sun–Thu, 20 Fri–Sat) for short stays under 50 pts
4. `bin/dvc search --from 2026-12-28 --to 2027-01-05 --budget 300` — cross-year boundary test
5. `bin/dvc list` — shows "VGF 2026, VGF 2027"
