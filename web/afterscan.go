package web

// afterScan runs the background work that follows a new library: covers, thumbnails and
// the lists the pages show, so opening a page for the first time is fast.
func (web *Web) afterScan() {
	web.startCoverDownloads()
	go web.warmPages()
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
