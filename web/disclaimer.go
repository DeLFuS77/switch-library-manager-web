package web

import "net/http"

// HandleDisclaimer shows what the app does not promise, linked from the footer of every page
// and from the setup wizard.
func (web *Web) HandleDisclaimer() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/disclaimer.html")
	web.router.HandleFunc("/disclaimer.html", func(w http.ResponseWriter, r *http.Request) {
		web.render(w, r, templates, web.globalPageData("disclaimer"))
	}).Methods("GET")
}
