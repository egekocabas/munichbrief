package location

// DistrictOutside and DistrictUnassigned account for reports that cannot be
// placed inside one official Munich district. Unknown names are unassigned;
// only a reviewed municipality relationship establishes an outside location.
const (
	DistrictOutside    = "outside"
	DistrictUnassigned = "unassigned"
)

// Districts returns the 25 official districts in name order. The values are
// copies so callers cannot change the location catalog.
func Districts() []Area {
	var out []Area
	for _, entity := range catalog {
		if entity.Kind == "district" && entity.Parent == "city:munich" {
			out = append(out, entity.Area)
		}
	}
	return out
}

func District(id string) (Area, bool) {
	entity, ok := parent(id)
	if !ok || entity.Kind != "district" || entity.Parent != "city:munich" {
		return Area{}, false
	}
	return entity.Area, true
}

var districtGroups = buildDistrictGroups()

func buildDistrictGroups() map[string]string {
	groups := make(map[string]string)
	for _, entity := range catalog {
		group := DistrictUnassigned
		current := entity
		for steps := 0; steps < len(catalog); steps++ {
			if current.Kind == "district" && current.Parent == "city:munich" {
				group = current.ID
				break
			}
			if current.Kind == "municipality" && current.ID != "city:munich" {
				group = DistrictOutside
				break
			}
			var ok bool
			current, ok = parent(current.Parent)
			if !ok {
				break
			}
		}
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			key := normalize(name)
			// If future catalog aliases become ambiguous, fail closed instead of
			// silently choosing whichever entity happens to be visited last.
			if previous, exists := groups[key]; exists && previous != group {
				groups[key] = DistrictUnassigned
			} else {
				groups[key] = group
			}
		}
	}
	return groups
}

// DistrictGroup maps an effective public area using exact catalog names and
// aliases, followed only by reviewed containment. It never infers a district
// from a substring, street hint, or the borders drawn by the map.
func DistrictGroup(areaName string) string {
	if group, ok := districtGroups[normalize(areaName)]; ok {
		return group
	}
	return DistrictUnassigned
}
