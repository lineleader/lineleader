// Package ledgerclient is a minimal HTTP client for the server's
// /api/v1/ledger JSON API (see docs/pitches/hosted-lineleader.md, "4. CLI
// becomes a thin client" and internal/web/api_handlers.go for the server
// side).
//
// It deliberately does NOT import internal/ledger: that package
// blank-imports the pgx Postgres driver (see internal/ledger/store.go), and
// because Go links a whole package's dependencies together, importing
// internal/ledger for even a single type or constant would pull the driver
// into any binary that imports this one. The CLI (cmd/dvc) must carry no
// database driver at all, so the wire DTOs and the handful of shared
// constants below are hand-duplicated from internal/ledger instead of
// imported. If the wire format or those constants change, both sides need
// updating by hand — there is no shared source of truth. Test files are
// exempt from this constraint (test binaries don't ship), and are free to
// import internal/ledger directly.
package ledgerclient

// Ledger entry kinds, mirroring internal/ledger's Kind* constants.
const (
	KindAllocation = "allocation"
	KindUsage      = "usage"
	KindBonus      = "bonus"
	KindSingleUse  = "single_use"
	KindAdjustment = "adjustment"
)

// DateLayout is the ledger's canonical date string format, mirroring
// internal/ledger.DateLayout.
const DateLayout = "2006-01-02"

// Entry is the wire shape of a ledger entry, as returned by the entries
// endpoints. RunningBalance is only populated by ListEntries (the list
// endpoint); it is the zero value on responses to Add/Update/Distribute.
type Entry struct {
	ID             int64  `json:"id"`
	UseYear        int    `json:"use_year"`
	Date           string `json:"date"`
	Desc           string `json:"description"`
	Kind           string `json:"kind"`
	Allotted       int    `json:"allotted"`
	Used           int    `json:"used"`
	Tag            string `json:"tag"`
	ContractID     *int64 `json:"contract_id"`
	RunningBalance int    `json:"running_balance"`
}

// EntryInput is the body accepted by POST/PUT entries requests.
type EntryInput struct {
	UseYear    int    `json:"use_year"`
	Date       string `json:"date"`
	Desc       string `json:"description"`
	Kind       string `json:"kind"`
	Allotted   int    `json:"allotted"`
	Used       int    `json:"used"`
	Tag        string `json:"tag"`
	ContractID *int64 `json:"contract_id"`
}

// Contract is the wire shape of a ledger contract.
type Contract struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Number       string `json:"number"`
	HomeResort   string `json:"home_resort"`
	AnnualPoints int    `json:"annual_points"`
	UseYearMonth string `json:"use_year_month"`
}

// ContractInput is the body accepted by POST contracts requests.
type ContractInput struct {
	Name         string `json:"name"`
	Number       string `json:"number"`
	HomeResort   string `json:"home_resort"`
	AnnualPoints int    `json:"annual_points"`
	UseYearMonth string `json:"use_year_month"`
}

// Summary is the wire shape of a per-use-year rollup.
type Summary struct {
	UseYear      int  `json:"use_year"`
	Allotted     int  `json:"allotted"`
	Used         int  `json:"used"`
	Net          int  `json:"net"`
	OverBorrowed bool `json:"over_borrowed"`
}
