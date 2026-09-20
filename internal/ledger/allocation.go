package ledger

import (
	"errors"
	"sort"
	"time"
)

// Disposition values a PointDraw can carry, ordered by the sequence
// AllocateStayPoints prefers them in: banked points expire soonest, so
// they're spent first; borrowed points come from next use year and are
// drawn last, since spending them early forfeits flexibility the member
// might not need.
const (
	DispositionBanked   = "banked"
	DispositionCurrent  = "current"
	DispositionBorrowed = "borrowed"
)

// ErrInsufficientPoints is returned by AllocateStayPoints when the supplied
// lots cannot fully fund a stay. The caller gets no partial allocation —
// see AllocateStayPoints for why.
var ErrInsufficientPoints = errors.New("insufficient points")

// PointLot is one contract's pool of points for one use year: the annual
// allotment (plus any bonus or single-use points folded into it), keyed by
// (ContractID, UseYear). AllocateStayPoints never sees the ledger directly —
// lots are the caller's summary of it, and Points is the lot's allotted
// total, not its remaining balance; remaining balance is computed inside
// AllocateStayPoints by subtracting consumed.
type PointLot struct {
	ContractID int64
	UseYear    int
	Points     int
}

// PointDraw records one lot's contribution to one stay: how many points of
// which disposition were drawn from which contract's use year. A stay may
// produce several draws when one lot's remaining balance can't cover the
// whole stay and allocation spills into the next candidate lot.
//
// PointDraw does double duty as AllocateStayPoints' output and (via consumed)
// its input: callers accumulate every draw ever made against a lot and pass
// that list back in so remaining balance reflects prior stays, without
// AllocateStayPoints ever touching storage itself.
type PointDraw struct {
	ContractID  int64
	UseYear     int
	Disposition string
	Points      int
}

// AllocateStayPoints is a pure function that decides which lots fund a stay,
// and how many points to draw from each. It is per-stay, not per-night: DVC
// prices a stay as a whole, so a single allocation walk covers every night
// at once rather than repeating the walk per night and re-summing draws
// against the same lots (the two are equivalent only because dispositions
// don't change mid-stay for the fixed checkIn date driving eligibility).
//
// Eligibility is computed per contract, not once globally: each contract has
// its own UseYearMonth (see Contract and UseYearForDate), so the same
// checkIn calendar date can fall in different use years for different
// contracts — an owner with an April contract and a December contract sees
// two different "current" use years for the same trip. For contract c, let
// uy = UseYearForDate(checkIn, c.UseYearMonth); a lot of that contract is a
// candidate only at use year uy-1 (DispositionBanked — expiring soonest),
// uy (DispositionCurrent) or uy+1 (DispositionBorrowed). A lot at any other
// use year, or whose ContractID isn't in contracts, is never a candidate. A
// lot whose remaining balance (its Points minus every consumed draw against
// the same ContractID+UseYear) is zero or negative is skipped entirely, even
// if it would otherwise be eligible.
//
// Candidates are built by walking each (ContractID, UseYear) once with its
// summed balance, ensuring multiple PointLot rows for the same contract and
// use year are coalesced into one candidate. Candidates are sorted by
// disposition rank (banked, then current, then borrowed), tie-broken by
// ContractID ascending, and drawn from in that order, each contributing
// min(need, remaining). This is deterministic regardless of the order
// contracts, lots or consumed are passed in — the same inputs always produce
// the same draws — which matters because the result is meant to be persisted
// as the stay's funding record.
//
// If the sorted candidates can't fully cover points, AllocateStayPoints
// returns nil, ErrInsufficientPoints rather than the partial draws it
// walked so far: a partial allocation would look like a real (if short)
// funding record to a careless caller, and the ledger has no representation
// for "this stay is 12 points short" short of refusing to allocate at all.
//
// points <= 0 is not an error — a comped or free stay needs no points — and
// returns nil, nil.
func AllocateStayPoints(contracts []Contract, lots []PointLot, consumed []PointDraw, checkIn time.Time, points int) ([]PointDraw, error) {
	if points <= 0 {
		return nil, nil
	}

	type lotKey struct {
		contractID int64
		useYear    int
	}

	months := make(map[int64]time.Month, len(contracts))
	for _, c := range contracts {
		months[c.ID] = c.UseYearMonth
	}

	remaining := make(map[lotKey]int, len(lots))
	for _, lot := range lots {
		remaining[lotKey{lot.ContractID, lot.UseYear}] += lot.Points
	}
	for _, draw := range consumed {
		remaining[lotKey{draw.ContractID, draw.UseYear}] -= draw.Points
	}

	type candidate struct {
		key         lotKey
		disposition string
		rank        int
	}

	var candidates []candidate
	seenKeys := make(map[lotKey]bool)
	for _, lot := range lots {
		key := lotKey{lot.ContractID, lot.UseYear}
		if remaining[key] <= 0 {
			continue
		}
		if seenKeys[key] {
			continue
		}
		month, ok := months[lot.ContractID]
		if !ok {
			continue
		}
		uy := UseYearForDate(checkIn, month)
		c := candidate{key: key}
		switch lot.UseYear {
		case uy - 1:
			c.disposition, c.rank = DispositionBanked, 0
		case uy:
			c.disposition, c.rank = DispositionCurrent, 1
		case uy + 1:
			c.disposition, c.rank = DispositionBorrowed, 2
		default:
			continue
		}
		seenKeys[key] = true
		candidates = append(candidates, c)
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		return candidates[i].key.contractID < candidates[j].key.contractID
	})

	need := points
	var result []PointDraw
	for _, c := range candidates {
		if need == 0 {
			break
		}
		available := remaining[c.key]
		draw := min(need, available)
		result = append(result, PointDraw{
			ContractID:  c.key.contractID,
			UseYear:     c.key.useYear,
			Disposition: c.disposition,
			Points:      draw,
		})
		remaining[c.key] -= draw
		need -= draw
	}

	if need != 0 {
		return nil, ErrInsufficientPoints
	}
	return result, nil
}
