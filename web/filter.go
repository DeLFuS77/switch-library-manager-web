package web

import (
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type TitleItemFilter struct {
	Keyword   string `in:"form=q"`
	PerPage   int    `in:"form=per_page;default=24"`
	SortBy    string `in:"form=sort_by;default=name"`
	SortOrder string `in:"form=sort_order;default=asc"`
	Page      int    `in:"form=page;default=1"`
	// library only: status of the games and file format
	Status string `in:"form=status"`
	Format string `in:"form=format"`
	// library only: games or demos, region, and games without a cover or unknown
	Kind   string `in:"form=kind"`
	Region string `in:"form=region"`
	Extra  string `in:"form=extra"`
}

// library status filters
const (
	STATUS_UPDATE   = "update"
	STATUS_DLC      = "dlc"
	STATUS_COMPLETE = "complete"
)

// library kind and extra filters
const (
	KIND_GAME      = "game"
	KIND_DEMO      = "demo"
	EXTRA_NO_COVER = "no_cover"
	EXTRA_UNKNOWN  = "unknown"
	EXTRA_RECENT   = "recent"
)

// games found in the folders in the last days count as recently added
const recentDays = 30

var (
	allowedPerPage = map[int]struct{}{12: {}, 24: {}, 48: {}, 96: {}}
	allowedSortBy  = map[string]struct{}{"added": {}, "id": {}, "latest_update_date": {}, "missing": {}, "name": {}, "region": {}, "release_date": {}, "size": {}, "type": {}}
	allowedStatus  = map[string]struct{}{"": {}, STATUS_UPDATE: {}, STATUS_DLC: {}, STATUS_COMPLETE: {}, STATUS_WANTED: {}}
	allowedKind    = map[string]struct{}{"": {}, KIND_GAME: {}, KIND_DEMO: {}}
	allowedExtra   = map[string]struct{}{"": {}, EXTRA_NO_COVER: {}, EXTRA_UNKNOWN: {}, EXTRA_RECENT: {}}
	regionPattern  = regexp.MustCompile(`^[A-Z]{2,4}$`)
)

// Normalize replaces invalid values coming from the query string with the defaults.
func (f *TitleItemFilter) Normalize() {
	f.Keyword = strings.TrimSpace(f.Keyword)
	if f.Page < 1 {
		f.Page = 1
	}
	if _, ok := allowedPerPage[f.PerPage]; !ok {
		f.PerPage = 24
	}
	if _, ok := allowedSortBy[f.SortBy]; !ok {
		f.SortBy = "name"
	}
	if f.SortOrder != "asc" && f.SortOrder != "desc" {
		f.SortOrder = "asc"
	}
	if _, ok := allowedStatus[f.Status]; !ok {
		f.Status = ""
	}
	// the format is compared with the formats of the library, anything else matches nothing
	f.Format = strings.ToUpper(strings.TrimSpace(f.Format))
	if len(f.Format) > 16 {
		f.Format = ""
	}
	if _, ok := allowedKind[f.Kind]; !ok {
		f.Kind = ""
	}
	if _, ok := allowedExtra[f.Extra]; !ok {
		f.Extra = ""
	}
	f.Region = strings.ToUpper(strings.TrimSpace(f.Region))
	if !regionPattern.MatchString(f.Region) {
		f.Region = ""
	}
}

// Active reports whether the items are filtered by a keyword, a status, a format or another
// library filter.
func (f *TitleItemFilter) Active() bool {
	return f.Keyword != "" || f.Status != "" || f.Format != "" || f.Kind != "" || f.Region != "" || f.Extra != ""
}

// query returns the query string of the filter, with the given values replaced; an empty
// value removes the parameter. Changing a filter goes back to the first page.
func (f *TitleItemFilter) query(replace ...string) url.Values {
	values := url.Values{}
	values.Set("q", f.Keyword)
	values.Set("status", f.Status)
	values.Set("format", strings.ToLower(f.Format))
	values.Set("kind", f.Kind)
	values.Set("region", strings.ToLower(f.Region))
	values.Set("extra", f.Extra)
	values.Set("per_page", strconv.Itoa(f.PerPage))
	values.Set("sort_by", f.SortBy)
	values.Set("sort_order", f.SortOrder)
	values.Set("page", strconv.Itoa(f.Page))
	for i := 0; i+1 < len(replace); i += 2 {
		values.Set(replace[i], replace[i+1])
		if replace[i] != "page" {
			values.Set("page", "1")
		}
	}
	for key, value := range values {
		if len(value) == 0 || value[0] == "" || (key == "page" && value[0] == "1") {
			values.Del(key)
		}
	}
	return values
}

// Matches reports whether the title ID or one of the names contains the keyword (case insensitive).
func (f *TitleItemFilter) Matches(id string, names ...string) bool {
	if f.Keyword == "" {
		return true
	}
	keyword := strings.ToLower(f.Keyword)
	if strings.Contains(strings.ToLower(id), keyword) {
		return true
	}
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), keyword) {
			return true
		}
	}
	return false
}

type TitleItemById               []TitleItem
type TitleItemByLatestUpdateDate []TitleItem
type TitleItemByMissingLen       []TitleItem
type TitleItemByName             []TitleItem
type TitleItemByRegion           []TitleItem
type TitleItemByReleaseDate      []TitleItem
type TitleItemByType             []TitleItem
type TitleItemBySize             []TitleItem
type TitleItemByAdded            []TitleItem

func (a TitleItemBySize) Len() int      { return len(a) }
func (a TitleItemBySize) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a TitleItemBySize) Less(i, j int) bool {
	if a[i].Size == a[j].Size {
		return lessName(a[i], a[j])
	}
	return a[i].Size < a[j].Size
}

func (a TitleItemByAdded) Len() int      { return len(a) }
func (a TitleItemByAdded) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a TitleItemByAdded) Less(i, j int) bool {
	if a[i].Added.Equal(a[j].Added) {
		return lessName(a[i], a[j])
	}
	return a[i].Added.Before(a[j].Added)
}

func (a TitleItemById) Len() int           { return len(a) }
func (a TitleItemById) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemById) Less(i, j int) bool {
	return a[i].Id < a[j].Id
}

func (a TitleItemByLatestUpdateDate) Len() int           { return len(a) }
func (a TitleItemByLatestUpdateDate) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByLatestUpdateDate) Less(i, j int) bool {
	if a[i].LatestUpdateDate == a[j].LatestUpdateDate {
		return lessName(a[i], a[j])
	}

	return a[i].LatestUpdateDate.Before(a[j].LatestUpdateDate)
}

func (a TitleItemByMissingLen) Len() int           { return len(a) }
func (a TitleItemByMissingLen) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByMissingLen) Less(i, j int) bool {
	if len(a[i].MissingDLC) == len(a[j].MissingDLC) {
		return lessName(a[i], a[j])
	}

	return len(a[i].MissingDLC) < len(a[j].MissingDLC)
}

func (a TitleItemByName) Len() int           { return len(a) }
func (a TitleItemByName) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByName) Less(i, j int) bool {
	return lessName(a[i], a[j])
}

func lessName(a, b TitleItem) bool {
	nameA, nameB := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if nameA == nameB {
		return a.Id < b.Id
	}
	return nameA < nameB
}

func (a TitleItemByRegion) Len() int           { return len(a) }
func (a TitleItemByRegion) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByRegion) Less(i, j int) bool {
	if a[i].Region == a[j].Region {
		return lessName(a[i], a[j])
	}

	return a[i].Region < a[j].Region
}

func (a TitleItemByReleaseDate) Len() int           { return len(a) }
func (a TitleItemByReleaseDate) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByReleaseDate) Less(i, j int) bool {
	if a[i].ReleaseDate == a[j].ReleaseDate {
		return lessName(a[i], a[j])
	}

	return a[i].ReleaseDate.Before(a[j].ReleaseDate)
}

func (a TitleItemByType) Len() int           { return len(a) }
func (a TitleItemByType) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a TitleItemByType) Less(i, j int) bool {
	if a[i].Type == a[j].Type {
		return lessName(a[i], a[j])
	}

	return a[i].Type < a[j].Type
}

func sortItems(filter *TitleItemFilter, items []TitleItem) error {
	var data sort.Interface

	switch filter.SortBy {
		case "id":
			data = TitleItemById(items)
		case "latest_update_date":
			data = TitleItemByLatestUpdateDate(items)
		case "missing":
			data = TitleItemByMissingLen(items)
		case "name":
			data = TitleItemByName(items)
		case "region":
			data = TitleItemByRegion(items)
		case "release_date":
			data = TitleItemByReleaseDate(items)
		case "type":
			data = TitleItemByType(items)
		case "size":
			data = TitleItemBySize(items)
		case "added":
			data = TitleItemByAdded(items)
		default:
			return errors.New("Unknown value for parameter sort_by")
	}

	if filter.SortOrder == "desc" {
		data = sort.Reverse(data)
	} else if filter.SortOrder != "asc" {
		return errors.New("Unknown value for parameter sort_order")
	}

	sort.Stable(data)

	return nil
}
