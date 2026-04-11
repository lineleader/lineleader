package dvc

import (
	"fmt"
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

// knownRoomTypes lists DVC room types with distinguishing keywords and metadata.
// THREE-BEDROOM must appear before TWO-BEDROOM and ONE-BEDROOM to avoid substring matches.
var knownRoomTypes = []struct {
	keyword string
	name    string
	sleeps  int
}{
	{"THREE-BEDROOM", "THREE-BEDROOM GRAND VILLA", 12},
	{"TWO-BEDROOM", "TWO-BEDROOM VILLA", 9},
	{"ONE-BEDROOM", "ONE-BEDROOM VILLA", 5},
	{"DELUXE STUDIO", "DELUXE STUDIO", 5},
	{"RESORT STUDIO", "RESORT STUDIO", 5},
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

// viewCodeRE matches isolated view codes: TP, R, or P.
// TP must come before R and P to avoid partial matches.
var viewCodeRE = regexp.MustCompile(`\b(TP|R|P)\b`)

// parseColumns extracts the ordered []Column from the header block (lines before TRAVEL PERIODS).
func parseColumns(headerLines []string) ([]Column, error) {
	// Step 1: Find the view-code line — the last header line containing "view" and view codes.
	viewLine := ""
	for _, line := range headerLines {
		if regexp.MustCompile(`(?i)(resort|preferred|theme park) view`).MatchString(line) {
			viewLine = line
		}
	}
	if viewLine == "" {
		return nil, fmt.Errorf("could not find view-code column header line")
	}

	// Step 2: Find where the legend ends (after the last "View" text).
	// Only view codes AFTER this point are actual column headers.
	legendEnd := 0
	if m := regexp.MustCompile(`(?i).*\bview\b`).FindStringIndex(viewLine); m != nil {
		legendEnd = m[1]
	}

	// Collect positions of view codes that appear after the legend.
	allViewPositions := viewCodeRE.FindAllStringIndex(viewLine, -1)
	var viewCodePositions [][]int
	var rawCodes []string
	for _, pos := range allViewPositions {
		if pos[0] >= legendEnd {
			viewCodePositions = append(viewCodePositions, pos)
			rawCodes = append(rawCodes, viewLine[pos[0]:pos[1]])
		}
	}
	if len(rawCodes) == 0 {
		return nil, fmt.Errorf("no view codes found after legend in: %q", viewLine)
	}

	// Step 3: Find the NIGHTS header line — contains "NIGHTS" and room type names.
	// This line is in the same horizontal coordinate space as viewLine.
	nightsLine := ""
	for _, hl := range headerLines {
		upper := strings.ToUpper(hl)
		if strings.Contains(upper, "NIGHTS") {
			for _, rt := range knownRoomTypes {
				if strings.Contains(upper, rt.keyword) {
					nightsLine = hl
					break
				}
			}
		}
		if nightsLine != "" {
			break
		}
	}
	if nightsLine == "" {
		return nil, fmt.Errorf("could not find NIGHTS header line")
	}

	// Step 4: Find character-position spans of each room type in the NIGHTS line.
	type roomSpan struct {
		name   string
		sleeps int
		startX int
		endX   int
	}
	var roomSpans []roomSpan
	upperNights := strings.ToUpper(nightsLine)
	for _, rt := range knownRoomTypes {
		if idx := strings.Index(upperNights, rt.keyword); idx >= 0 {
			roomSpans = append(roomSpans, roomSpan{rt.name, rt.sleeps, idx, idx + len(rt.keyword)})
		}
	}
	// Sort by startX ascending.
	for i := 1; i < len(roomSpans); i++ {
		for j := i; j > 0 && roomSpans[j].startX < roomSpans[j-1].startX; j-- {
			roomSpans[j], roomSpans[j-1] = roomSpans[j-1], roomSpans[j]
		}
	}
	if len(roomSpans) == 0 {
		return nil, fmt.Errorf("no room types found in NIGHTS line")
	}

	// Step 5: Assign each view code to its room type.
	// Room type labels are LEFT-ALIGNED within their column group, so we use a
	// boundary approach: a view code belongs to the room type whose startX is the
	// largest startX that is still ≤ the view code's position.
	// roomSpans is already sorted by startX ascending.
	var columns []Column
	for i, pos := range viewCodePositions {
		codeX := pos[0]
		code := rawCodes[i]

		// Find the rightmost room span that starts at or before this view code.
		best := roomSpans[0]
		for _, rs := range roomSpans[1:] {
			if rs.startX <= codeX {
				best = rs
			}
		}
		columns = append(columns, Column{RoomType: best.name, View: code, Sleeps: best.sleeps})
	}

	return columns, nil
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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

	columns, err := parseColumns(lines[:splitIdx])
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
		isWeekly := strings.Contains(upper, "WEEKLY")

		dates := parseDateRangesFromLine(line, year)

		switch {
		case isSunThu:
			flush()
			pending.sunThu = parseInts(line)
			pending.dates = append(pending.dates, dates...)
		case isFriSat:
			pending.friSat = parseInts(line)
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
