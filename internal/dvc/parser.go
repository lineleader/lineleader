package dvc

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// parseColumns extracts the ordered []Column from the header block (lines before TRAVEL PERIODS).
func parseColumns(headerLines []string) ([]Column, error) {
	// Step 1: Find the NIGHTS header line and room-type spans within it.
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

	type roomSpan struct {
		name   string
		sleeps int
		startX int
	}
	var roomSpans []roomSpan
	upperNights := strings.ToUpper(nightsLine)
	for _, rt := range knownRoomTypes {
		// Find all occurrences so duplicate room-type keywords (e.g. two TWO-BEDROOM
		// columns with different views) all get their own span.
		for start := 0; ; {
			idx := strings.Index(upperNights[start:], rt.keyword)
			if idx < 0 {
				break
			}
			abs := start + idx
			roomSpans = append(roomSpans, roomSpan{rt.name, rt.sleeps, abs})
			start = abs + 1
		}
	}
	sort.Slice(roomSpans, func(i, j int) bool { return roomSpans[i].startX < roomSpans[j].startX })
	if len(roomSpans) == 0 {
		return nil, fmt.Errorf("no room types found in NIGHTS line")
	}

	// Step 2: Find the column-code line — the header line with the most view-code matches.
	// A legend line has only 1 match; the actual column header has N×M matches (rooms × views).
	// Require at least 2 matches to distinguish a real column-code line from a legend line.
	codeLine := ""
	bestCount := 1
	for _, line := range headerLines {
		n := len(viewCodeRE.FindAllString(line, -1))
		if n > bestCount {
			bestCount = n
			codeLine = line
		}
	}

	if codeLine == "" {
		// No view-code columns: one Column per room-type span.
		var cols []Column
		for _, rs := range roomSpans {
			cols = append(cols, Column{RoomType: rs.name, View: "", Sleeps: rs.sleeps})
		}
		return cols, nil
	}

	// Step 3: On the column-code line some matches may be part of the legend prefix
	// (e.g. "TP - Theme Park View   R P TP …" or "C - Kilimanjaro …   V R SV C …").
	// Compute legendEnd as the byte offset after the last "CODE -" legend prefix.
	legendEnd := 0
	for _, m := range legendCodeRE.FindAllStringIndex(codeLine, -1) {
		if m[1] > legendEnd {
			legendEnd = m[1]
		}
	}

	var rawCodes []string
	var codePositions []int
	for _, pos := range viewCodeRE.FindAllStringIndex(codeLine, -1) {
		if pos[0] >= legendEnd {
			rawCodes = append(rawCodes, codeLine[pos[0]:pos[1]])
			codePositions = append(codePositions, pos[0])
		}
	}

	if len(rawCodes) == 0 {
		// All matches were inside the legend prefix — treat as no-view-code.
		var cols []Column
		for _, rs := range roomSpans {
			cols = append(cols, Column{RoomType: rs.name, View: "", Sleeps: rs.sleeps})
		}
		return cols, nil
	}

	// Step 4: Assign each view code to the room-type whose startX is the largest
	// value still ≤ the code's position (left-anchored column groups).
	var columns []Column
	for i, code := range rawCodes {
		codeX := codePositions[i]
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
