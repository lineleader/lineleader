package dvc

import "slices"

// EffectiveFilters resolves the filter set a trip should search with: its own
// set when overriding, otherwise the global config's set.
func EffectiveFilters(global Config, mode FilterMode, f FilterSet) FilterSet {
	if mode == FilterModeOverride {
		return f
	}
	return global.AsFilterSet()
}

// stayEquals compares two StayResults by identity fields (Resort, RoomType,
// View, CheckIn, CheckOut, Points). Nights is not compared: it's derived from
// CheckIn/CheckOut so it can't disagree once those match. Points IS compared
// — two rows can otherwise collide on every other field yet cost different
// points (e.g. duplicate chart entries for the same resort/room/view), and
// treating those as "the same stay" would make selection-tracking code
// deselect instead of switching the selection when the user clicks the other
// row.
func stayEquals(a, b StayResult) bool {
	return a.Resort == b.Resort &&
		a.RoomType == b.RoomType &&
		a.View == b.View &&
		a.CheckIn.Equal(b.CheckIn) &&
		a.CheckOut.Equal(b.CheckOut) &&
		a.Points == b.Points
}

// CloneFilterSet returns a deep copy of f with freshly allocated slices, so
// the result shares no backing arrays with f. The web layer uses this to seed
// a trip's per-trip override filters from the global config without aliasing
// the global config's slices.
func CloneFilterSet(f FilterSet) FilterSet {
	return FilterSet{
		ExcludeResorts:   append([]string(nil), f.ExcludeResorts...),
		ExcludeRoomTypes: append([]string(nil), f.ExcludeRoomTypes...),
	}
}

// FilterOptionsView is the de-duplicated, sorted resort + room-type option
// lists for one filter scope. Scope (global vs. a particular trip) is a web
// concern — see web.filterScope — so this carries no scope of its own.
type FilterOptionsView struct {
	Resorts   []ResortOption
	RoomTypes []RoomTypeOption
}

// ResortOption is one selectable resort in a FilterOptionsView.
type ResortOption struct {
	Code, Name string
	Enabled    bool
}

// RoomTypeOption is one selectable room type in a FilterOptionsView.
type RoomTypeOption struct {
	Name    string
	Enabled bool
}

// FilterOptionsFor returns the de-duplicated, sorted resort + room-type option
// lists across all charts, with Enabled meaning "not in excluded".
func FilterOptionsFor(charts []*ResortChart, excluded FilterSet) FilterOptionsView {
	var view FilterOptionsView

	resortNames := map[string]string{} // code → full name
	roomSeen := map[string]bool{}
	var resortCodes, roomTypes []string
	for _, c := range charts {
		if _, seen := resortNames[c.ResortCode]; !seen {
			resortNames[c.ResortCode] = c.ResortName
			resortCodes = append(resortCodes, c.ResortCode)
		}
		for _, col := range c.Columns {
			if !roomSeen[col.RoomType] {
				roomSeen[col.RoomType] = true
				roomTypes = append(roomTypes, col.RoomType)
			}
		}
	}
	slices.Sort(resortCodes)
	slices.Sort(roomTypes)

	for _, code := range resortCodes {
		view.Resorts = append(view.Resorts, ResortOption{
			Code:    code,
			Name:    resortNames[code],
			Enabled: !slices.Contains(excluded.ExcludeResorts, code),
		})
	}
	for _, rt := range roomTypes {
		view.RoomTypes = append(view.RoomTypes, RoomTypeOption{
			Name:    rt,
			Enabled: !slices.Contains(excluded.ExcludeRoomTypes, rt),
		})
	}
	return view
}
