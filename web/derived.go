package web

import (
	"sync"

	"github.com/dtrunk90/switch-library-manager-web/pagination"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// derivedCache keeps results computed from the library and the settings, such as the
// missing updates or the items of a page, until either changes. With a big library these
// take tens of milliseconds and are needed by every page (the navigation counts), so they
// are computed once instead of on every request.
type derivedCache struct {
	mutex           sync.Mutex
	stateVersion    uint64
	settingsVersion uint64
	values          map[string]any
}

// derived returns the cached value of name, computing it if the library or the settings
// changed since it was computed. Cached values are shared: callers must not modify them.
func (web *Web) derived(name string, compute func() any) any {
	stateVersion, settingsVersion := web.state.Version(), settings.Version()

	cache := &web.cache
	cache.mutex.Lock()
	if cache.values == nil || cache.stateVersion != stateVersion || cache.settingsVersion != settingsVersion {
		cache.values = map[string]any{}
		cache.stateVersion, cache.settingsVersion = stateVersion, settingsVersion
	}
	if value, ok := cache.values[name]; ok {
		cache.mutex.Unlock()
		return value
	}
	cache.mutex.Unlock()

	// computed without the lock, so slow pages do not block each other
	value := compute()

	cache.mutex.Lock()
	// a value computed while the library or the settings changed may be outdated
	if cache.stateVersion == stateVersion && cache.settingsVersion == settingsVersion &&
		web.state.Version() == stateVersion && settings.Version() == settingsVersion {
		cache.values[name] = value
	}
	cache.mutex.Unlock()
	return value
}

// sorted returns the cached items of a list (name) in the order of the filter. Filtering
// keeps the order, so each request only walks the sorted list instead of sorting it.
func (web *Web) sorted(name string, filter *TitleItemFilter, build func() []TitleItem) []TitleItem {
	return web.derived(name+"|"+filter.SortBy+"|"+filter.SortOrder, func() any {
		all := web.derived(name, func() any { return build() }).([]TitleItem)
		// a copy: the unsorted list is shared
		items := append([]TitleItem(nil), all...)
		if err := sortItems(filter, items); err != nil {
			web.sugarLogger.Error(err)
		}
		return items
	}).([]TitleItem)
}

// invalidateDerived drops the cached results, after a change they do not follow
// automatically (e.g. a verification).
func (web *Web) invalidateDerived() {
	web.cache.mutex.Lock()
	web.cache.values = nil
	web.cache.mutex.Unlock()
}

// filterPage returns the page of the sorted items that match the keyword of the filter.
func (web *Web) filterPage(filter *TitleItemFilter, sorted []TitleItem) ([]TitleItem, pagination.Pagination) {
	items := sorted
	if filter.Keyword != "" {
		items = make([]TitleItem, 0, len(sorted))
		for _, item := range sorted {
			if filter.Matches(item.Id, item.Name, item.OriginalName) {
				items = append(items, item)
			}
		}
	}

	p := pagination.Calculate(filter.Page, filter.PerPage, len(items))
	// a copy of the page, so the cached list is never shared with a template
	return append([]TitleItem(nil), items[p.Start:p.End]...), p
}
