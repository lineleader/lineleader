package dvc

import (
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParsePDF extracts and parses a DVC point chart PDF, returning a ResortChart.
// Requires pdftotext to be installed.
func ParsePDF(pdfPath string) (*ResortChart, error) {
	text, err := extractText(pdfPath)
	if err != nil {
		return nil, err
	}
	code := extractResortCode(filepath.Base(pdfPath))
	return parseLayoutText(text, code)
}

// extractText runs pdftotext -layout on pdfPath and returns its stdout.
func extractText(pdfPath string) (string, error) {
	out, err := exec.Command("pdftotext", "-layout", pdfPath, "-").Output()
	if err != nil {
		return "", fmt.Errorf("pdftotext: %w", err)
	}
	return string(out), nil
}

// extractResortCode derives a short resort code from a filename.
// e.g. "VGF-2026.pdf" → "VGF", "2027_VGF.pdf" → "VGF"
func extractResortCode(filename string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	parts := regexp.MustCompile(`[-_]`).Split(name, -1)
	for _, p := range parts {
		if regexp.MustCompile(`^[A-Z]{2,5}$`).MatchString(p) {
			return p
		}
	}
	return name
}

// parseInts extracts all integer values from s in left-to-right order.
func parseInts(s string) []int {
	var result []int
	for _, tok := range regexp.MustCompile(`\d+`).FindAllString(s, -1) {
		n, _ := strconv.Atoi(tok)
		result = append(result, n)
	}
	return result
}

// parseIntsAfterKeyword extracts integers from s that appear AFTER the first
// occurrence of keyword. This prevents date numbers on the same line (e.g.,
// "Jan 1 - Jan 31  FRI—SAT  20  24...") from contaminating point values.
func parseIntsAfterKeyword(s, keyword string) []int {
	idx := strings.Index(strings.ToUpper(s), strings.ToUpper(keyword))
	if idx < 0 {
		return parseInts(s)
	}
	return parseInts(s[idx+len(keyword):])
}

// intPos is an integer value together with the byte offset (in the line) of its
// first digit — used as a column x-anchor when aligning header labels to data.
type intPos struct {
	val int
	x   int
}

// parseIntPositionsAfterKeyword is the positional variant of parseIntsAfterKeyword.
// It returns each integer with the byte offset of its first digit within s.
func parseIntPositionsAfterKeyword(s, keyword string) []intPos {
	offset := 0
	idx := strings.Index(strings.ToUpper(s), strings.ToUpper(keyword))
	if idx >= 0 {
		offset = idx + len(keyword)
	}
	var out []intPos
	for _, m := range regexp.MustCompile(`\d+`).FindAllStringIndex(s[offset:], -1) {
		n, _ := strconv.Atoi(s[offset+m[0] : offset+m[1]])
		out = append(out, intPos{val: n, x: offset + m[0]})
	}
	return out
}

// knownRoomTypes lists DVC room types with distinguishing keywords and metadata.
// Longer/more-specific keywords must appear before shorter ones to avoid substring matches.
var knownRoomTypes = []struct {
	keyword string
	name    string
	sleeps  int
}{
	{"THREE-BEDROOM TREEHOUSE", "THREE-BEDROOM TREEHOUSE VILLA", 9},
	{"THREE-BEDROOM BEACH COTTAGE", "THREE-BEDROOM BEACH COTTAGE", 12},
	{"THREE-BEDROOM", "THREE-BEDROOM GRAND VILLA", 12},
	{"TWO-BEDROOM BEACH COTTAGE", "TWO-BEDROOM BEACH COTTAGE", 12},
	{"TWO-BEDROOM CABIN", "TWO-BEDROOM CABIN", 8},
	{"TWO-BEDROOM BUNGALOW", "TWO-BEDROOM BUNGALOW", 8},
	{"TWO-BEDROOM PENTHOUSE", "TWO-BEDROOM PENTHOUSE VILLA", 8},
	{"TWO-BEDROOM", "TWO-BEDROOM VILLA", 9},
	{"ONE-BEDROOM", "ONE-BEDROOM VILLA", 5},
	{"DELUXE INN ROOM", "DELUXE INN ROOM", 4},
	{"DELUXE STUDIO", "DELUXE STUDIO", 5},
	{"TOWER STUDIO", "TOWER STUDIO", 2},
	{"RESORT STUDIO", "RESORT STUDIO", 5},
	{"DUO STUDIO", "DUO STUDIO", 2},
	{"HOTEL ROOM", "HOTEL ROOM", 4},
	{"CABIN", "CABIN", 6},
}

// dateRangeRE matches "Month D - Month D" patterns.
var dateRangeRE = regexp.MustCompile(
	`(?i)(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)\s+(\d{1,2})\s*-\s*(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Sept|Oct|Nov|Dec)\s+(\d{1,2})`,
)

var monthMap = map[string]time.Month{
	"jan": time.January, "feb": time.February, "mar": time.March,
	"apr": time.April, "may": time.May, "jun": time.June,
	"jul": time.July, "aug": time.August, "sep": time.September,
	"sept": time.September, "oct": time.October, "nov": time.November,
	"dec": time.December,
}

func parseMonth(s string) time.Month { return monthMap[strings.ToLower(s)] }

func parseDateRangesFromLine(line string, year int) []DateRange {
	matches := dateRangeRE.FindAllStringSubmatch(line, -1)
	var result []DateRange
	for _, m := range matches {
		start := time.Date(year, parseMonth(m[1]), mustAtoi(m[2]), 0, 0, 0, 0, time.UTC)
		end := time.Date(year, parseMonth(m[3]), mustAtoi(m[4]), 0, 0, 0, 0, time.UTC)
		result = append(result, DateRange{
			Start: start.Format("2006-01-02"),
			End:   end.Format("2006-01-02"),
		})
	}
	return result
}

func mustAtoi(s string) int { n, _ := strconv.Atoi(s); return n }

// viewCodeRE matches isolated DVC view codes. TP/SV/PM must appear before single-letter
// alternatives to prevent partial matches. B/P uses a literal slash (no word boundary needed).
var viewCodeRE = regexp.MustCompile(`\b(TP|SV|PM|R|P|S|V|C|I|O)\b|B/P`)

// legendCodeRE matches a line that starts with a view code definition: "CODE - description"
var legendCodeRE = regexp.MustCompile(`^\s*[A-Z]{1,3}(?:/[A-Z]{1,2})?\s*-\s*\S`)

// cellRE matches a "cell" on a layout line — a run of non-space tokens joined by single
// spaces. Two-or-more spaces break cells. E.g. "DELUXE STUDIO   ONE-BEDROOM" → two cells.
var cellRE = regexp.MustCompile(`\S+(?: \S+)*`)

// sleepsRE captures N in "(Sleeps up to N)".
var sleepsRE = regexp.MustCompile(`\(Sleeps up to (\d+)\)`)

// cell is a non-empty text fragment on a header line with byte offsets in that line.
type cell struct {
	text  string
	start int // inclusive
	end   int // exclusive
}

func (c cell) center() int { return (c.start + c.end) / 2 }

// parseCells splits line into cells separated by 2+ spaces.
func parseCells(line string) []cell {
	var out []cell
	for _, m := range cellRE.FindAllStringIndex(line, -1) {
		out = append(out, cell{text: line[m[0]:m[1]], start: m[0], end: m[1]})
	}
	return out
}

// sleepsInCell returns N if the cell is a "(Sleeps up to N)" annotation, else 0.
func sleepsInCell(c cell) int {
	if m := sleepsRE.FindStringSubmatch(c.text); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

// matchRoomType scans text (upper-cased) for known room-type keywords in declared order
// (longest first wins). Returns (canonical name, sleeps, matched keyword, found).
func matchRoomType(text string) (string, int, string, bool) {
	upper := strings.ToUpper(text)
	for _, rt := range knownRoomTypes {
		if strings.Contains(upper, rt.keyword) {
			return rt.name, rt.sleeps, rt.keyword, true
		}
	}
	return "", 0, "", false
}

// hasDuplicateKeyword reports whether any known room-type keyword appears 2+ times in text —
// e.g. SSR's "THREE-BEDROOM THREE-BEDROOM" NIGHTS-line cell where the second occurrence
// denotes a distinct room variant (TREEHOUSE VILLA) disambiguated on a later header line.
func hasDuplicateKeyword(text string) bool {
	upper := strings.ToUpper(text)
	for _, rt := range knownRoomTypes {
		if strings.Count(upper, rt.keyword) >= 2 {
			return true
		}
	}
	return false
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// parseColumns extracts []Column anchored on the first data row's integer x-positions.
// The data row defines the ground-truth column count and positions; header lines above
// it (the block preceding TRAVEL PERIODS) are walked to determine each column's room
// type, view, and sleeps.
func parseColumns(headerLines []string, firstDataLine string) ([]Column, error) {
	// 1. Column anchors from the first data row.
	keyword := "THU"
	if !strings.Contains(strings.ToUpper(firstDataLine), "THU") {
		keyword = "SAT"
	}
	dataCols := parseIntPositionsAfterKeyword(firstDataLine, keyword)
	if len(dataCols) == 0 {
		return nil, fmt.Errorf("no integer columns in data row")
	}

	// 2. Find NIGHTS header line and its group cells (column-group labels).
	nightsIdx := -1
	for i, hl := range headerLines {
		if strings.Contains(strings.ToUpper(hl), "NIGHTS") {
			nightsIdx = i
			break
		}
	}
	if nightsIdx < 0 {
		return nil, fmt.Errorf("could not find NIGHTS header line")
	}
	nightsLine := headerLines[nightsIdx]
	firstDataX := dataCols[0].x
	var groupCells []cell
	for _, c := range parseCells(nightsLine) {
		// Skip cells fully left of the data-column region (row labels like "NIGHTS").
		if c.end+5 < firstDataX {
			continue
		}
		if strings.TrimSpace(strings.ToUpper(c.text)) == "NIGHTS" {
			continue
		}
		groupCells = append(groupCells, c)
	}
	if len(groupCells) == 0 {
		return nil, fmt.Errorf("no column groups on NIGHTS line")
	}

	// 3. Group horizontal spans. Matched NIGHTS labels (e.g. "DELUXE STUDIO") are
	//    left-aligned over their column group, so next-cell.start is the correct
	//    boundary. Unmatched group headers (e.g. VDH "GARDEN ROOM") span multiple
	//    sub-columns and are center-aligned, so their boundary is the midpoint of
	//    the whitespace gap with the previous cell (biased right by 1 so a column
	//    landing exactly on the midpoint falls in the left group).
	type groupRange struct {
		start, end int // end is exclusive
	}
	matchedCell := make([]bool, len(groupCells))
	for i, gc := range groupCells {
		_, _, _, matchedCell[i] = matchRoomType(gc.text)
	}
	boundaryAt := func(prev, next cell, nextMatched bool) int {
		if nextMatched {
			return next.start
		}
		return (prev.end + next.start + 1) / 2
	}
	ranges := make([]groupRange, len(groupCells))
	for i := range groupCells {
		gr := groupRange{start: math.MinInt32 / 2, end: math.MaxInt32 / 2}
		if i > 0 {
			gr.start = boundaryAt(groupCells[i-1], groupCells[i], matchedCell[i])
		}
		if i < len(groupCells)-1 {
			gr.end = boundaryAt(groupCells[i], groupCells[i+1], matchedCell[i+1])
		}
		ranges[i] = gr
	}

	// 4. View codes and their x positions (excluding the legend prefix).
	codeLine := ""
	bestCount := 1
	for _, line := range headerLines {
		n := len(viewCodeRE.FindAllString(line, -1))
		if n > bestCount {
			bestCount = n
			codeLine = line
		}
	}
	type viewCode struct {
		text string
		x    int
	}
	var codes []viewCode
	if codeLine != "" {
		legendEnd := 0
		for _, m := range legendCodeRE.FindAllStringIndex(codeLine, -1) {
			if m[1] > legendEnd {
				legendEnd = m[1]
			}
		}
		for _, pos := range viewCodeRE.FindAllStringIndex(codeLine, -1) {
			if pos[0] >= legendEnd {
				codes = append(codes, viewCode{text: codeLine[pos[0]:pos[1]], x: pos[0]})
			}
		}
	}

	// Helper: closest "(Sleeps up to N)" annotation to x within range (returns 0 if none).
	findSleepsNear := func(x int, gr groupRange) int {
		bestN, bestD := 0, math.MaxInt32
		for _, hl := range headerLines {
			for _, c := range parseCells(hl) {
				n := sleepsInCell(c)
				if n == 0 {
					continue
				}
				if c.center() < gr.start || c.center() >= gr.end {
					continue
				}
				d := absInt(x - c.center())
				if d < bestD {
					bestD, bestN = d, n
				}
			}
		}
		return bestN
	}

	// 5. Build the column list group-by-group.
	var columns []Column
	for gi, gc := range groupCells {
		gr := ranges[gi]
		var colsInGroup []intPos
		for _, dp := range dataCols {
			if dp.x >= gr.start && dp.x < gr.end {
				colsInGroup = append(colsInGroup, dp)
			}
		}
		if len(colsInGroup) == 0 {
			continue
		}
		var codesInGroup []viewCode
		for _, vc := range codes {
			if vc.x >= gr.start && vc.x < gr.end {
				codesInGroup = append(codesInGroup, vc)
			}
		}

		baseName, baseSleeps, baseKeyword, matched := matchRoomType(gc.text)

		if !matched {
			// Group-prefix case (e.g. VDH "GARDEN ROOM"): find sub-room-type cells on
			// subsequent header lines within this group's span and combine.
			prefix := strings.TrimSpace(gc.text)
			var subCells []cell
			for li, hl := range headerLines {
				if li == nightsIdx {
					continue
				}
				for _, c := range parseCells(hl) {
					if c.center() < gr.start || c.center() >= gr.end {
						continue
					}
					if sleepsInCell(c) > 0 {
						continue
					}
					if _, _, _, ok := matchRoomType(c.text); ok {
						subCells = append(subCells, c)
					}
				}
			}
			for _, dp := range colsInGroup {
				if len(subCells) == 0 {
					columns = append(columns, Column{RoomType: prefix, View: "", Sleeps: 0})
					continue
				}
				best := subCells[0]
				bestD := absInt(dp.x - best.center())
				for _, sc := range subCells[1:] {
					if d := absInt(dp.x - sc.center()); d < bestD {
						best, bestD = sc, d
					}
				}
				subName, subSleeps, _, _ := matchRoomType(best.text)
				combined := prefix + " " + subName
				if cName, cSleeps, _, ok := matchRoomType(combined); ok {
					combined, subSleeps = cName, cSleeps
				}
				if n := findSleepsNear(dp.x, gr); n > 0 {
					subSleeps = n
				}
				columns = append(columns, Column{RoomType: combined, View: "", Sleeps: subSleeps})
			}
			continue
		}

		if hasDuplicateKeyword(gc.text) && len(colsInGroup) > len(codesInGroup) {
			// Duplicate-keyword case (e.g. SSR "THREE-BEDROOM THREE-BEDROOM"): the
			// view-code columns use the default variant; remaining no-view columns use
			// an alternate variant. The distinguishing token (e.g. "TREEHOUSE") lives
			// in a sub-cell on a later header line — combine it with the base keyword
			// and re-match to find the compound known room type.
			altName, altSleeps := "", baseSleeps
			for li, hl := range headerLines {
				if li == nightsIdx {
					continue
				}
				for _, c := range parseCells(hl) {
					if c.center() < gr.start || c.center() >= gr.end {
						continue
					}
					if sleepsInCell(c) > 0 {
						continue
					}
					for _, word := range strings.Fields(c.text) {
						n, s, _, ok := matchRoomType(baseKeyword + " " + word)
						if ok && n != baseName {
							altName, altSleeps = n, s
							break
						}
					}
					if altName != "" {
						break
					}
				}
				if altName != "" {
					break
				}
			}
			for i, dp := range colsInGroup {
				name, sleeps, view := baseName, baseSleeps, ""
				if i < len(codesInGroup) {
					view = codesInGroup[i].text
				} else if altName != "" {
					name, sleeps = altName, altSleeps
				}
				if n := findSleepsNear(dp.x, gr); n > 0 {
					sleeps = n
				}
				columns = append(columns, Column{RoomType: name, View: view, Sleeps: sleeps})
			}
			continue
		}

		// Standard case: 1:1 mapping of data cols to view codes (or no-view if none).
		for i, dp := range colsInGroup {
			view := ""
			if i < len(codesInGroup) {
				view = codesInGroup[i].text
			}
			sleeps := baseSleeps
			if n := findSleepsNear(dp.x, gr); n > 0 {
				sleeps = n
			}
			columns = append(columns, Column{RoomType: baseName, View: view, Sleeps: sleeps})
		}
	}
	return columns, nil
}

type pendingSeason struct {
	dates  []DateRange
	sunThu []int
	friSat []int
}

func (p *pendingSeason) complete() bool {
	return len(p.sunThu) > 0 && len(p.friSat) > 0 && len(p.dates) > 0
}

func (p *pendingSeason) toSeason() Season {
	return Season{Periods: p.dates, SunThu: p.sunThu, FriSat: p.friSat}
}

func (p *pendingSeason) reset() {
	p.dates = nil
	p.sunThu = nil
	p.friSat = nil
}

// parseLayoutText parses the pdftotext -layout output into a ResortChart.
func parseLayoutText(text string, resortCode string) (*ResortChart, error) {
	lines := strings.Split(text, "\n")
	if len(lines) == 0 {
		return nil, fmt.Errorf("empty PDF text")
	}

	resortName := ""
	for _, l := range lines {
		if s := strings.TrimSpace(l); s != "" {
			resortName = s
			break
		}
	}

	year := 0
	yearRE := regexp.MustCompile(`(\d{4})\s+VACATION POINTS PER NIGHT`)
	for _, l := range lines {
		if m := yearRE.FindStringSubmatch(l); m != nil {
			year, _ = strconv.Atoi(m[1])
			break
		}
	}
	if year == 0 {
		return nil, fmt.Errorf("could not find year in PDF text")
	}

	// Split at "TRAVEL PERIODS" (or first SUN—THU line as fallback).
	splitIdx := -1
	firstSunThuIdx := -1
	for i, l := range lines {
		if strings.Contains(l, "TRAVEL PERIODS") && splitIdx < 0 {
			splitIdx = i
		}
		if firstSunThuIdx < 0 && strings.Contains(l, "SUN") && strings.Contains(l, "THU") {
			firstSunThuIdx = i
		}
	}
	if splitIdx < 0 {
		splitIdx = firstSunThuIdx
	}
	if splitIdx < 0 {
		return nil, fmt.Errorf("could not find data section start")
	}

	// Locate the first data row (SUN—THU or SUN—SAT) to anchor column positions.
	firstDataLine := ""
	for _, l := range lines[splitIdx:] {
		upper := strings.ToUpper(l)
		if strings.Contains(upper, "SUN") && (strings.Contains(upper, "THU") || strings.Contains(upper, "SAT")) {
			firstDataLine = l
			break
		}
	}
	if firstDataLine == "" {
		return nil, fmt.Errorf("could not find first data row")
	}

	columns, err := parseColumns(lines[:splitIdx], firstDataLine)
	if err != nil {
		return nil, fmt.Errorf("parsing columns: %w", err)
	}

	seasons, err := parseSeasons(lines[splitIdx:], year)
	if err != nil {
		return nil, fmt.Errorf("parsing seasons: %w", err)
	}

	return &ResortChart{
		ResortName: resortName,
		ResortCode: resortCode,
		Year:       year,
		Columns:    columns,
		Seasons:    seasons,
	}, nil
}

// parseSeasons extracts all seasons from the data section of the layout text.
func parseSeasons(dataLines []string, year int) ([]Season, error) {
	var seasons []Season
	var pending pendingSeason

	flush := func() {
		if pending.complete() {
			seasons = append(seasons, pending.toSeason())
		}
		pending.reset()
	}

	for _, line := range dataLines {
		upper := strings.ToUpper(line)
		isSunThu := strings.Contains(upper, "SUN") && strings.Contains(upper, "THU")
		isFriSat := strings.Contains(upper, "FRI") && strings.Contains(upper, "SAT")
		// SUN—SAT: uniform nightly rate (e.g. AULANI) — same value for both Sun-Thu and Fri-Sat.
		isSunSat := strings.Contains(upper, "SUN") && strings.Contains(upper, "SAT") &&
			!strings.Contains(upper, "THU") && !strings.Contains(upper, "FRI")
		isWeekly := strings.Contains(upper, "WEEKLY")

		dates := parseDateRangesFromLine(line, year)

		switch {
		case isSunThu:
			flush()
			pending.sunThu = parseIntsAfterKeyword(line, "THU")
			pending.dates = append(pending.dates, dates...)
		case isSunSat:
			flush()
			vals := parseIntsAfterKeyword(line, "SAT")
			pending.sunThu = vals
			pending.friSat = vals
			pending.dates = append(pending.dates, dates...)
		case isFriSat:
			pending.friSat = parseIntsAfterKeyword(line, "SAT")
			pending.dates = append(pending.dates, dates...)
		case isWeekly:
			pending.dates = append(pending.dates, dates...)
			flush()
		case len(dates) > 0:
			pending.dates = append(pending.dates, dates...)
		}
	}
	flush()

	return seasons, nil
}
