package ledger

// PricePerPointYear derives this contract's amortised acquisition cost per
// point per year: (purchase price + closing costs) / (annual points × term
// years), in Micros. It is never stored — only ever computed from the
// stored inputs.
//
// ok is false ("cost unknown") when any of TermYears, PurchasePrice or
// ClosingCosts is zero (the pre-backfill state for every contract) or when
// AnnualPoints is zero, which would otherwise divide by zero.
func (c Contract) PricePerPointYear() (Micros, bool) {
	if c.TermYears <= 0 || c.PurchasePrice <= 0 || c.ClosingCosts <= 0 || c.AnnualPoints <= 0 {
		return 0, false
	}
	num := (int64(c.PurchasePrice) + int64(c.ClosingCosts)) * microsPerCent
	den := int64(c.AnnualPoints) * int64(c.TermYears)
	return Micros(divRound(num, den)), true
}
