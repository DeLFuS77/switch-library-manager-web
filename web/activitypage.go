package web

import (
	"encoding/csv"
	"net/http"
	"sort"
	"strconv"
)

// ActivityPageData is the activity page: the actions, and the users and kinds to filter by.
type ActivityPageData struct {
	GlobalPageData
	Activity []Activity
	Users    []string
	Actions  []ActivityActionName
}

// ActivityActionName is a kind of action with its name, for the filter.
type ActivityActionName struct {
	Value string
	Name  string
}

// activityActionNames are the kinds of actions with their names, to translate, for the filter.
func activityActionNames(lang string) []ActivityActionName {
	names := make([]ActivityActionName, 0, len(activityActions))
	for _, action := range activityActions {
		names = append(names, ActivityActionName{Value: action, Name: translate(lang, action)})
	}
	sort.Slice(names, func(i, j int) bool { return names[i].Name < names[j].Name })
	return names
}

// activityUsers are the names seen in the log, for the filter.
func activityUsers(entries []Activity) []string {
	seen := map[string]bool{}
	users := []string{}
	for _, entry := range entries {
		if entry.User != "" && !seen[entry.User] {
			seen[entry.User] = true
			users = append(users, entry.User)
		}
	}
	sort.Strings(users)
	return users
}

func (web *Web) HandleActivity() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/activity.html")

	web.router.HandleFunc("/activity.html", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		entries := web.activities().list()
		web.render(w, r, templates, ActivityPageData{
			GlobalPageData: web.globalPageData("activity"),
			Activity:       translateActivity(lang, entries),
			Users:          activityUsers(entries),
			Actions:        activityActionNames(lang),
		})
	}).Methods("GET")

	// the whole log as a spreadsheet; the detail stays in English, like the stored log. The names
	// tried at the login page and the values of the forms are never read as formulas
	web.router.HandleFunc("/activity.csv", func(w http.ResponseWriter, r *http.Request) {
		lang := web.requestLanguage(r)
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote("activity.csv"))
		writer := csv.NewWriter(w)
		writer.Write([]string{"time", "user", "action", "detail", "ip"})
		for _, entry := range translateActivity(lang, web.activities().list()) {
			writer.Write([]string{entry.Time.Format("2006-01-02 15:04:05"), csvSafe(entry.User), translate(lang, entry.Action), csvSafe(entry.Detail), csvSafe(entry.IP)})
		}
		writer.Flush()
	}).Methods("GET")
}
