package web

import "github.com/dtrunk90/switch-library-manager-web/db"

// afterScan runs the background work that follows a new library: covers, thumbnails and
// the lists the pages show, so opening a page for the first time is fast.
func (web *Web) afterScan() {
	web.startCoverDownloads()
	web.afterScanWork.Add(1)
	go func() {
		defer web.afterScanWork.Done()
		// first: the lists show the dates the games were added
		web.recordHistory()
		web.warmPages()
	}()
}

// warmPages builds the lists of the main pages in the default languages before anyone asks.
func (web *Web) warmPages() {
	if web.state.IsSynchronizing() {
		return
	}
	web.navCounts()
	web.compressedCopies()
	for _, lang := range web.titleLanguagesWithDefault() {
		filter := &TitleItemFilter{}
		filter.Normalize()
		web.getLibraryWithFacets(filter, lang)
		web.getMissingUpdates(filter, lang)
		web.getMissingDLC(filter, lang)
		web.getMissingGames(filter, lang)
	}
}

// titleLanguagesWithDefault are the languages pages are shown in: the default one and the
// languages in use.
func (web *Web) titleLanguagesWithDefault() []string {
	return append([]string{DEFAULT_LANGUAGE}, web.titleLanguages()...)
}

// PartialLibrary shows the games a slow scan found so far (see db.PartialLibraryReceiver).
// The scan goes on and replaces it with the whole library at the end.
func (web *Web) PartialLibrary(titles map[string]*db.SwitchGameFiles, skipped map[db.ExtendedFileInfo]db.SkippedFile, files int) {
	web.state.setPartialLibrary(&db.LocalSwitchFilesDB{TitlesMap: titles, Skipped: skipped, NumFiles: files})
}

// setPartialLibrary replaces the library while the synchronization keeps running.
func (s *WebState) setPartialLibrary(localDB *db.LocalSwitchFilesDB) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !s.isSynchronizing {
		return
	}
	s.localDB = localDB
	s.version++
}
