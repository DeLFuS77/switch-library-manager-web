package web

import (
	"sort"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/i18n"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The filter On the console of the Library, in the group Yours of its panel: for each console of
// the user who looks that told what is installed on it, the games installed on it, the ones that
// are not, and the ones the library has something newer of for it (as the page Consoles counts
// them). Its choice is in the address (on_console=installed.<id>), so the Catalogue and the links
// keep it.

// how long the Library waits for the list of files of the library to count the games out of date
// on the consoles; past that the choice is not offered, unless it is the one asked for
const libraryConsolesWait = 300 * time.Millisecond

// ConsoleChoice is a console in the filter On the console, with how many games each of its
// choices leaves.
type ConsoleChoice struct {
	Id           string
	Name         string
	Installed    int
	NotInstalled int
	Outdated     int
	// the games out of date are counted: the choice is offered
	Counted bool
}

// libraryConsole is a console of the user, for the filter: the games installed on it and the ones
// out of date, by the prefix of their title IDs.
type libraryConsole struct {
	choice    ConsoleChoice
	installed map[string]bool
	outdated  map[string]bool
}

// libraryConsoles are the consoles of a user that told what is installed on them, by their names,
// with what the library has newer for them when its list of files is ready.
func (web *Web) libraryConsoles(user string, filter *TitleItemFilter) []*libraryConsole {
	if user == "" || web.auth == nil {
		return nil
	}
	keys := web.netUserConsoles(user)
	sort.SliceStable(keys, func(i, j int) bool { return strings.ToLower(keys[i].Name) < strings.ToLower(keys[j].Name) })
	state, _ := filter.onConsoleChoice()
	switchDB, _ := web.state.get()
	consoles := []*libraryConsole{}
	var kept []netFile
	listed, ready := false, false
	for _, key := range keys {
		inventory, ok := web.readNetInventory(key.ID)
		if !ok {
			continue
		}
		console := &libraryConsole{choice: ConsoleChoice{Id: key.ID, Name: key.Name}, installed: map[string]bool{}, outdated: map[string]bool{}}
		for _, item := range inventory.Items {
			if prefix := netGamePrefix(item.TitleId); prefix != "" {
				console.installed[prefix] = true
			}
		}
		if !listed {
			listed = true
			wait := libraryConsolesWait
			if state == ON_CONSOLE_OUTDATED {
				wait = netConsolesCountWait
			}
			kept, _, ready = web.netFilesReady(settings.ReadSettings(web.dataFolder).NetInstall, wait)
		}
		if ready {
			games, _ := compareNetConsole(inventory, kept, switchDB)
			for _, game := range games {
				if game.Outdated {
					console.outdated[netGamePrefix(game.TitleId)] = true
				}
			}
			console.choice.Counted = true
		}
		consoles = append(consoles, console)
	}
	return consoles
}

// onConsoleLabel is the choice of the filter On the console, as its chip and the Catalogue tell it.
func onConsoleLabel(filter *TitleItemFilter, consoles []*libraryConsole, lang string) string {
	state, id := filter.onConsoleChoice()
	if id == "" {
		return ""
	}
	for _, console := range consoles {
		if console.choice.Id != id {
			continue
		}
		switch state {
		case ON_CONSOLE_INSTALLED:
			return i18n.Translatef(lang, "Installed on %v", console.choice.Name)
		case ON_CONSOLE_NOT:
			return i18n.Translatef(lang, "Not installed on %v", console.choice.Name)
		case ON_CONSOLE_OUTDATED:
			return i18n.Translatef(lang, "Out of date on %v", console.choice.Name)
		}
	}
	return i18n.Translate(lang, "On the console")
}

// onConsole counts the game as installed or not, and out of date, on each console of the user,
// and takes it with the choice of the filter; a console that is not one of the user's takes none.
func (f *libraryFilter) onConsole(item *TitleItem) bool {
	state, id := f.filter.onConsoleChoice()
	if len(f.consoles) == 0 {
		return id == ""
	}
	prefix := netGamePrefix(item.Id)
	taken := id == ""
	for _, console := range f.consoles {
		installed, outdated := console.installed[prefix], console.outdated[prefix]
		if installed {
			console.choice.Installed++
		} else {
			console.choice.NotInstalled++
		}
		if outdated {
			console.choice.Outdated++
		}
		if console.choice.Id == id {
			switch state {
			case ON_CONSOLE_INSTALLED:
				taken = installed
			case ON_CONSOLE_NOT:
				taken = !installed
			case ON_CONSOLE_OUTDATED:
				taken = outdated
			}
		}
	}
	return taken
}
