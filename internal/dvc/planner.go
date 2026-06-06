package dvc

// EffectiveFilters resolves the filter set a trip should search with: its own
// set when overriding, otherwise the global config's set.
func EffectiveFilters(global Config, mode FilterMode, f FilterSet) FilterSet {
	if mode == FilterModeOverride {
		return f
	}
	return global.AsFilterSet()
}
