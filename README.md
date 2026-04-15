# LineLeader

LineLeader is a Disney tools platform satisfying unique needs of both
beginner and expert Disney vacation enthusiast.

## DVC Search

Find every possible stay at a Disney Vacation Club resort that fits within
a points budget across a flexible travel window.

### Setup

Build the binary and import your point chart PDFs:

```sh
make dvc
./bin/dvc import path/to/VGF-2026.pdf path/to/VGF-2027.pdf
```

Imported data is saved as JSON in `data/point-charts/` and can be committed
to the repo. Run `make import` to import the bundled VGF 2026 and 2027 charts.

### Usage

```
dvc import [--data-dir PATH] <pdf-file> [pdf-files...]
dvc search --from DATE --to DATE --budget N [--min-nights N] [--data-dir PATH]
dvc list   [--data-dir PATH]
```

**Search for stays:**

```sh
# All stays at VGF in January 2026 under 100 points
./bin/dvc search --from 2026-01-01 --to 2026-01-31 --budget 100

# At least a 4-night stay over spring break
./bin/dvc search --from 2026-03-15 --to 2026-03-30 --budget 200 --min-nights 4
```

Results are sorted by points (ascending) and show resort, room type, view,
check-in/out dates, nights, and total points.

**Interactive TUI:**

`dvc search` launches an interactive terminal UI. Use Tab to move between
fields and Enter to run a search. Press `f` to open the filter panel, which
lists all imported resorts (by full name) and room types as toggleable items:

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `space` / `x` | Toggle resort or room type |
| `f` / `esc` | Close filter panel |

Filters are applied live. Excluded items are saved to
`~/.config/lineleader/config.json` and loaded on next launch.

**Show available data:**

```sh
./bin/dvc list
```

**Config file:**

Default exclusions can be set in `~/.config/lineleader/config.json`:

```json
{
  "exclude_resorts": ["AKV", "BCV"],
  "exclude_room_types": ["3-Bedroom Grand Villa"]
}
```

Resort codes match what `dvc list` shows. The filter panel uses full resort
names for display but stores codes internally.

### Adding more resorts

Run `dvc import` on any DVC point chart PDF (standard Walt Disney World
resort format). The tool extracts room types, view categories, and all
seasonal date ranges automatically.

### Requirements

- `pdftotext` (from [poppler-utils](https://poppler.freedesktop.org/)) must
  be installed for `dvc import`
- Go 1.26+
