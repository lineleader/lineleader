package dvc

import (
	"fmt"
	"io"
	"text/tabwriter"
)

// PrintTable writes a formatted table of search results to w.
func PrintTable(w io.Writer, results []StayResult, params SearchParams) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintln(tw, "RESORT\tROOM TYPE\tVIEW\tCHECK-IN\tCHECK-OUT\tNIGHTS\tPTS\t")
	fmt.Fprintln(tw, "------\t---------\t----\t--------\t---------\t------\t---\t")

	for _, r := range results {
		view := r.View
		if view == "" {
			view = "—"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t\n",
			r.Resort,
			r.RoomType,
			view,
			r.CheckIn.Format("2006-01-02"),
			r.CheckOut.Format("2006-01-02"),
			r.Nights,
			r.Points,
		)
	}
	tw.Flush()

	noun := "results"
	if len(results) == 1 {
		noun = "result"
	}
	fmt.Fprintf(w, "\n%d %s | Budget: %d pts | Window: %s – %s\n",
		len(results),
		noun,
		params.Budget,
		params.WindowStart.Format("2006-01-02"),
		params.WindowEnd.Format("2006-01-02"),
	)
}
