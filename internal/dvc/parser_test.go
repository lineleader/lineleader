package dvc

import (
	"testing"
)

// vgf2026Layout is a trimmed excerpt of the pdftotext -layout output for VGF-2026.pdf
// used to test parsing without requiring the PDF file at test time.
const vgf2026Layout = `The Villas at Disney's Grand Floridian Resort & Spa
AT WALT DISNEY WORLD® RESORT


2026 VACATION POINTS PER NIGHT
                                        NIGHTS                     RESORT STUDIO                       DELUXE STUDIO                   ONE-BEDROOM                    TWO-BEDROOM                  THREE-BEDROOM
                                                                    (Sleeps up to 5)                      (Sleeps up to 5)                     VILLA                          VILLA                   GRAND VILLA
                                                                                                                                          (Sleeps up to 5)               (Sleeps up to 9)              (Sleeps up to 12)
   R - Resort View
   P - Preferred View
   TP - Theme Park View                                       R              P             TP              R               P              R               P               R              P                      P

TRAVEL PERIODS
                                       SUN—THU                16            19             24             16              19              31             39              44              54                    111
                                        FRI—SAT               20            24             27             20              24              41             48              55              65                    131
Sept 1 - Sept 30
                                         WEEKLY              120            143            174            120            143             237             291            330             400                    817



                                       SUN—THU                17            21             25             17              21              36             43              49              59                    118
Jan 1 - Jan 31                          FRI—SAT               20            24             29             20              24              44             51              58              68                    138
May 1 - May 14                           WEEKLY              125            153            183            125            153             268             317            361             431                    866



                                       SUN—THU                18            21             26             18              21              38             46              53              62                    126
May 15 - Jun 10                         FRI—SAT               21            26             31             21              26              46             55              61              74                    148
Dec 1 - Dec 23                           WEEKLY              132            157            192            132            157             282             340            387             458                    926



                                       SUN—THU                18            22             28             18              22              41             49              56              66                    131
Feb 1 - Feb 15                          FRI—SAT               21            27             32             21              27              48             57              65              78                    155
Jun 11 - Aug 31                          WEEKLY              132            164            204            132            164             301             359            410             486                    965



                                       SUN—THU                22            26             32             22              26              43             53              61              73                    143
Oct 1 - Nov 24
                                        FRI—SAT               24            29             36             24              29              51             61              69              82                    169
Nov 28 - Nov 30
                                         WEEKLY              158            188            232            158            188             317             387            443             529                   1053



                                       SUN—THU                24            27             34             24              27              46             55              65              75                    161
Feb 16 - Mar 28
                                        FRI—SAT               26            32             41             26              32              55             66              75              88                    187
Apr 6 - Apr 30
Nov 25 - Nov 27                          WEEKLY              172            199            252            172            199             340             407            475             551                   1179



                                       SUN—THU                32            38             47             32              38              64             76              87             103                    197
Mar 29 - Apr 5
                                        FRI—SAT               37            44             54             37              44              75             89             103             122                    227
Dec 24 - Dec 31
                                         WEEKLY              234            278            343            234            278             470             558            641             759                   1439
`

func TestParseLayoutText_VGF2026(t *testing.T) {
	chart, err := parseLayoutText(vgf2026Layout, "VGF")
	if err != nil {
		t.Fatalf("parseLayoutText error: %v", err)
	}

	if chart.ResortCode != "VGF" {
		t.Errorf("ResortCode = %q, want %q", chart.ResortCode, "VGF")
	}
	if chart.Year != 2026 {
		t.Errorf("Year = %d, want 2026", chart.Year)
	}
	if chart.ResortName != "The Villas at Disney's Grand Floridian Resort & Spa" {
		t.Errorf("ResortName = %q", chart.ResortName)
	}

	// Expect 10 columns: R P TP R P R P R P P
	if len(chart.Columns) != 10 {
		t.Fatalf("len(Columns) = %d, want 10", len(chart.Columns))
	}
	wantCols := []Column{
		{RoomType: "RESORT STUDIO", View: "R", Sleeps: 5},
		{RoomType: "RESORT STUDIO", View: "P", Sleeps: 5},
		{RoomType: "RESORT STUDIO", View: "TP", Sleeps: 5},
		{RoomType: "DELUXE STUDIO", View: "R", Sleeps: 5},
		{RoomType: "DELUXE STUDIO", View: "P", Sleeps: 5},
		{RoomType: "ONE-BEDROOM VILLA", View: "R", Sleeps: 5},
		{RoomType: "ONE-BEDROOM VILLA", View: "P", Sleeps: 5},
		{RoomType: "TWO-BEDROOM VILLA", View: "R", Sleeps: 9},
		{RoomType: "TWO-BEDROOM VILLA", View: "P", Sleeps: 9},
		{RoomType: "THREE-BEDROOM GRAND VILLA", View: "P", Sleeps: 12},
	}
	for i, want := range wantCols {
		got := chart.Columns[i]
		if got != want {
			t.Errorf("Columns[%d] = %+v, want %+v", i, got, want)
		}
	}

	// Expect 7 seasons
	if len(chart.Seasons) != 7 {
		t.Fatalf("len(Seasons) = %d, want 7", len(chart.Seasons))
	}

	// Season 0: Sept 1 - Sept 30, SunThu col 0 = 16, FriSat col 0 = 20
	s0 := chart.Seasons[0]
	if len(s0.Periods) != 1 {
		t.Errorf("Season[0] periods = %d, want 1", len(s0.Periods))
	} else if s0.Periods[0].Start != "2026-09-01" || s0.Periods[0].End != "2026-09-30" {
		t.Errorf("Season[0] period = %v", s0.Periods[0])
	}
	if s0.SunThu[0] != 16 {
		t.Errorf("Season[0].SunThu[0] = %d, want 16", s0.SunThu[0])
	}
	if s0.FriSat[0] != 20 {
		t.Errorf("Season[0].FriSat[0] = %d, want 20", s0.FriSat[0])
	}
	// Three-bedroom grand villa col 9 SunThu = 111
	if s0.SunThu[9] != 111 {
		t.Errorf("Season[0].SunThu[9] = %d, want 111", s0.SunThu[9])
	}

	// Season 1: Jan 1 - Jan 31 and May 1 - May 14 (2 periods)
	s1 := chart.Seasons[1]
	if len(s1.Periods) != 2 {
		t.Errorf("Season[1] periods = %d, want 2", len(s1.Periods))
	}

	// Season 4: Oct 1 - Nov 24 and Nov 28 - Nov 30 (2 periods)
	s4 := chart.Seasons[4]
	if len(s4.Periods) != 2 {
		t.Errorf("Season[4] periods = %d, want 2", len(s4.Periods))
	}

	// Season 5: Feb 16 - Mar 28, Apr 6 - Apr 30, Nov 25 - Nov 27 (3 periods)
	s5 := chart.Seasons[5]
	if len(s5.Periods) != 3 {
		t.Errorf("Season[5] periods = %d, want 3", len(s5.Periods))
	}
}

func TestExtractResortCode(t *testing.T) {
	cases := []struct {
		filename string
		want     string
	}{
		{"VGF-2026.pdf", "VGF"},
		{"2027_VGF.pdf", "VGF"},
		{"AKV-2026.pdf", "AKV"},
		{"2027_BLT.pdf", "BLT"},
	}
	for _, c := range cases {
		got := extractResortCode(c.filename)
		if got != c.want {
			t.Errorf("extractResortCode(%q) = %q, want %q", c.filename, got, c.want)
		}
	}
}

func TestParseInts(t *testing.T) {
	cases := []struct {
		s    string
		want []int
	}{
		{"SUN—THU                16            19             24", []int{16, 19, 24}},
		{"                                         WEEKLY              120            143            174", []int{120, 143, 174}},
		{"no numbers here", []int{}},
	}
	for _, c := range cases {
		got := parseInts(c.s)
		if len(got) != len(c.want) {
			t.Errorf("parseInts(%q) = %v, want %v", c.s, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("parseInts(%q)[%d] = %d, want %d", c.s, i, got[i], c.want[i])
			}
		}
	}
}
