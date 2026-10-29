package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Smart collections are filters of the Library kept under a name ("Co-op for four", "Playing
// and short"...): their games are found again each time, so they fill and empty by themselves.

const (
	SMART_COLLECTIONS_FILENAME = "smartcollections.json"
	maxSmartCollections        = 50
)

// the parameters of the Library a smart collection keeps; the page and its size are not kept
var smartCollectionKeys = []string{"q", "status", "format", "kind", "region", "extra", "collection", "missing", "genre", "players", "languages", "play", "sort_by", "sort_order"}

// SmartCollection is a filter of the Library with a name.
type SmartCollection struct {
	Name  string `json:"name"`
	Query string `json:"query"`
}

type smartCollectionStore struct {
	mutex sync.Mutex
	path  string
	items []SmartCollection
}

func (web *Web) smartCollections() *smartCollectionStore {
	web.smartCollectionsOnce.Do(func() {
		store := &smartCollectionStore{path: filepath.Join(web.dataFolder, SMART_COLLECTIONS_FILENAME)}
		store.reload()
		web.smartCollectionsStore = store
	})
	return web.smartCollectionsStore
}

// reload reads them again, also after a backup was restored.
func (s *smartCollectionStore) reload() {
	items := []SmartCollection{}
	if data, ok := readStore(s.path); ok {
		parseStore(s.path, data, &items)
	}
	if items == nil {
		items = []SmartCollection{}
	}
	s.mutex.Lock()
	s.items = items
	s.mutex.Unlock()
}

func (s *smartCollectionStore) list() []SmartCollection {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return append([]SmartCollection(nil), s.items...)
}

func (s *smartCollectionStore) save() error {
	data, err := json.Marshal(s.items)
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data)
}

// put keeps a smart collection, replacing the one of the same name.
func (s *smartCollectionStore) put(collection SmartCollection) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for i, item := range s.items {
		if strings.EqualFold(item.Name, collection.Name) {
			s.items[i] = collection
			return s.save()
		}
	}
	if len(s.items) >= maxSmartCollections {
		return errTooManySmartCollections
	}
	s.items = append(s.items, collection)
	sort.Slice(s.items, func(i, j int) bool { return strings.ToLower(s.items[i].Name) < strings.ToLower(s.items[j].Name) })
	return s.save()
}

func (s *smartCollectionStore) remove(name string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	items := s.items[:0]
	for _, item := range s.items {
		if !strings.EqualFold(item.Name, name) {
			items = append(items, item)
		}
	}
	s.items = items
	return s.save()
}

var errTooManySmartCollections = &smartError{"There are too many smart collections: delete one first."}

type smartError struct{ text string }

func (e *smartError) Error() string { return e.text }

// cleanSmartQuery keeps the filters of a query of the Library alone, in a fixed order; "" when
// it filters nothing.
func cleanSmartQuery(query string) string {
	values, err := url.ParseQuery(strings.TrimPrefix(query, "?"))
	if err != nil {
		return ""
	}
	filter := &TitleItemFilter{Keyword: values.Get("q"), Status: values.Get("status"), Format: values.Get("format"), Kind: values.Get("kind"),
		Region: values.Get("region"), Extra: values.Get("extra"), Collection: values.Get("collection"), WithMissing: values.Get("missing"),
		Genre: values.Get("genre"), Players: values.Get("players"), GameLanguage: values.Get("languages"), Play: values.Get("play"),
		SortBy: values.Get("sort_by"), SortOrder: values.Get("sort_order"), PerPage: 24, Page: 1}
	filter.Normalize()
	if !filter.Filters() {
		return ""
	}
	clean := filter.query()
	kept := url.Values{}
	for _, key := range smartCollectionKeys {
		if value := clean.Get(key); value != "" {
			kept.Set(key, value)
		}
	}
	return kept.Encode()
}

// SmartCollectionView is a smart collection on the Library page: where it leads, how many games
// it has now, and whether it is the one shown.
type SmartCollectionView struct {
	Name   string
	Href   string
	Count  int
	Active bool
}

// smartCollectionViews are the smart collections with their games now, for a user and the
// filter shown.
func (web *Web) smartCollectionViews(filter *TitleItemFilter, lang string) []SmartCollectionView {
	views := []SmartCollectionView{}
	current := cleanSmartQuery(filter.query().Encode())
	for _, collection := range web.smartCollections().list() {
		values, _ := url.ParseQuery(collection.Query)
		saved := &TitleItemFilter{Keyword: values.Get("q"), Status: values.Get("status"), Format: values.Get("format"), Kind: values.Get("kind"),
			Region: values.Get("region"), Extra: values.Get("extra"), Collection: values.Get("collection"), WithMissing: values.Get("missing"),
			Genre: values.Get("genre"), Players: values.Get("players"), GameLanguage: values.Get("languages"), Play: values.Get("play"),
			SortBy: values.Get("sort_by"), SortOrder: values.Get("sort_order"), PerPage: 24, Page: 1}
		saved.Normalize()
		saved.player = filter.player
		_, p, _ := web.getLibraryWithFacets(saved, lang)
		views = append(views, SmartCollectionView{Name: collection.Name, Href: "/index.html?" + collection.Query, Count: p.NumItems, Active: collection.Query == current})
	}
	return views
}

func (web *Web) HandleSmartCollections() {
	web.router.HandleFunc("/smart-collections", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		name := cleanCollectionName(r.FormValue("name"))
		if name == "" {
			writeGlobalError(w, http.StatusBadRequest, lang, "Invalid collection name")
			return
		}
		var err error
		if r.FormValue("action") == "delete" {
			err = web.smartCollections().remove(name)
		} else {
			query := cleanSmartQuery(r.FormValue("query"))
			if query == "" {
				writeGlobalError(w, http.StatusBadRequest, lang, "Choose some filters first.")
				return
			}
			err = web.smartCollections().put(SmartCollection{Name: name, Query: query})
		}
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, lang, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": name})
	}).Methods("POST")
}
