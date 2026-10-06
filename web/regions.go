package web

import (
	"sort"
	"strings"
)

// The same game is sold in several stores with a different title ID for each one: the
// European and the American copy are two games for the console. They are found by their
// names (see regionalName) and one copy is suggested to keep.

// RegionCopy is a copy of a game of the library sold in one store.
type RegionCopy struct {
	Id        string
	Name      string
	Region    string
	Languages []string
	// the language of the interface is one of the languages of the game
	HasLanguage bool
	// the update and the DLC of the library for this copy
	LocalUpdate int
	Dlc         int
	Size        int64
	// the copy to keep, and why
	Keep    bool
	Reasons []string
}

// RegionGroup is a game of the library with copies of several stores.
type RegionGroup struct {
	Name   string
	Copies []RegionCopy
	// the space of the copies that are not suggested to keep
	Savable int64
}

// reasons to keep a copy, translated by the interface
const (
	REGION_REASON_LANGUAGE  = "In your language"
	REGION_REASON_LANGUAGES = "More languages"
	REGION_REASON_UPDATE    = "Newer update"
	REGION_REASON_DLC       = "More DLC"
)

func (web *Web) regionalDuplicates(lang string) []RegionGroup {
	return web.derived("regionalDuplicates:"+lang, func() any { return web.buildRegionalDuplicates(lang) }).([]RegionGroup)
}

func (web *Web) buildRegionalDuplicates(lang string) []RegionGroup {
	switchDB, localDB := web.state.get()
	if switchDB == nil || localDB == nil {
		return nil
	}
	byName := map[string][]RegionCopy{}
	for prefix, local := range localDB.TitlesMap {
		if !local.BaseExist || local.File.Metadata == nil {
			continue
		}
		title := switchDB.TitlesMap[prefix]
		original := getLocalTitleName(title, local)
		key := regionalName(original)
		if key == "" {
			continue
		}
		copy := RegionCopy{
			Id:          strings.ToUpper(local.File.Metadata.TitleId),
			Name:        titleName(switchDB, lang, local.File.Metadata.TitleId, original),
			LocalUpdate: local.LatestUpdate,
			Dlc:         len(local.Dlc),
			Size:        local.File.ExtendedInfo.Size,
		}
		for _, update := range local.Updates {
			copy.Size += update.ExtendedInfo.Size
		}
		for _, dlc := range local.Dlc {
			copy.Size += dlc.ExtendedInfo.Size
		}
		if title != nil {
			copy.Region = title.Attributes.Region
			copy.Languages = title.Attributes.Languages
			copy.HasLanguage = hasValue(title.Attributes.Languages, lang)
		}
		byName[key] = append(byName[key], copy)
	}

	groups := []RegionGroup{}
	for _, copies := range byName {
		if len(copies) < 2 {
			continue
		}
		groups = append(groups, suggestRegionCopy(copies))
	}
	sort.Slice(groups, func(i, j int) bool { return strings.ToLower(groups[i].Name) < strings.ToLower(groups[j].Name) })
	return groups
}

// suggestRegionCopy marks the copy to keep: the one in the language of the interface, then
// the one with more languages, the newer update and more DLC.
func suggestRegionCopy(copies []RegionCopy) RegionGroup {
	score := func(c RegionCopy) int {
		value := len(c.Languages)*10 + c.Dlc
		if c.HasLanguage {
			value += 1000
		}
		if c.LocalUpdate > 0 {
			value += 5
		}
		return value
	}
	sort.SliceStable(copies, func(i, j int) bool {
		if score(copies[i]) != score(copies[j]) {
			return score(copies[i]) > score(copies[j])
		}
		return copies[i].Id < copies[j].Id
	})
	best := &copies[0]
	best.Keep = true
	others := copies[1:]
	reason := func(text string, better func(other RegionCopy) bool) {
		for _, other := range others {
			if !better(other) {
				return
			}
		}
		best.Reasons = append(best.Reasons, text)
	}
	reason(REGION_REASON_LANGUAGE, func(other RegionCopy) bool { return best.HasLanguage && !other.HasLanguage })
	reason(REGION_REASON_LANGUAGES, func(other RegionCopy) bool { return len(best.Languages) > len(other.Languages) })
	reason(REGION_REASON_UPDATE, func(other RegionCopy) bool { return best.LocalUpdate > other.LocalUpdate })
	reason(REGION_REASON_DLC, func(other RegionCopy) bool { return best.Dlc > other.Dlc })

	group := RegionGroup{Name: best.Name, Copies: copies}
	for _, other := range others {
		group.Savable += other.Size
	}
	return group
}
