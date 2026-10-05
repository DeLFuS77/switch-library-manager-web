package web

import (
	"errors"
	"sort"
	"strings"
)

type TitleItemFilter struct {
	Keyword   string `in:"form=q"`
	PerPage   int    `in:"form=per_page;default=24"`
	SortBy    string `in:"form=sort_by;default=name"`
	SortOrder string `in:"form=sort_order;default=asc"`
	Page      int    `in:"form=page;default=1"`
}

var (
	allowedPerPage = map[int]struct{}{12: {}, 24: {}, 48: {}, 96: {}}
	allowedSortBy  = map[string]struct{}{"id": {}, "latest_update_date": {}, "missing": {}, "name": {}, "region": {}, "release_date": {}, "type": {}}
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
}

// Matches reports whether the title ID or name contains the keyword (case insensitive).
func (f *TitleItemFilter) Matches(id string, name string) bool {
	if f.Keyword == "" {
		return true
	}
	keyword := strings.ToLower(f.Keyword)
	return strings.Contains(strings.ToLower(id), keyword) || strings.Contains(strings.ToLower(name), keyword)
}

type TitleItemById               []TitleItem
type TitleItemByLatestUpdateDate []TitleItem
type TitleItemByMissingLen       []TitleItem
type TitleItemByName             []TitleItem
type TitleItemByRegion           []TitleItem
type TitleItemByReleaseDate      []TitleItem
type TitleItemByType             []TitleItem

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
