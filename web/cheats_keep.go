package web

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// An old update can be the only version of a game with cheats: the newer one has none yet.
// It is kept then, unless the settings say otherwise, so the game can be installed at the
// version its cheats are for.

const NOTE_KEPT_FOR_CHEATS = "There are cheats for this version and not for the newer one: it is kept."

// updateInName is the title ID and the version in the name of a file, "[0100...800][v65536]".
var updateInName = regexp.MustCompile(`(?i)\[([0-9a-f]{16})\]\s*\[v(\d+)\]`)

// keptForCheats tells why an old update stays because of its cheats, or "". The cheats need
// one copy of the version, the one the library holds: another copy of it goes once that one
// was checked sound.
func (web *Web) keptForCheats(file db.ExtendedFileInfo) string {
	if settings.ReadSettings(web.dataFolder).OrganizeOptions.DeleteUpdatesWithCheats {
		return ""
	}
	cheats := web.cheats()
	_, localDB := web.state.get()
	if cheats == nil || localDB == nil {
		return ""
	}
	id, version, ok := updateOf(file)
	if !ok {
		return ""
	}
	// cheats are for a game and its updates: an old DLC (v0, as the game) is no update of it
	if kind, err := db.ContentKind(id); err != nil || kind != db.KIND_UPDATE {
		return ""
	}
	prefix, err := db.TitleIDPrefix(id)
	if err != nil {
		return ""
	}
	game := strings.ToUpper(prefix) + "000"
	latest := 0
	local := localDB.TitlesMap[strings.ToLower(prefix)]
	if local != nil {
		latest = local.LatestUpdate
	}
	if version == latest || !cheats.has(game, version) || cheats.has(game, latest) {
		return ""
	}
	if local != nil {
		if held, ok := local.Updates[version]; ok {
			heldPath := filepath.Join(held.ExtendedInfo.BaseFolder, held.ExtendedInfo.FileName)
			if heldPath != filepath.Join(file.BaseFolder, file.FileName) {
				entry := SpaceFile{KeptBy: heldPath, Deletable: true}
				web.requireSoundKeptCopy(&entry)
				if entry.Deletable {
					return ""
				}
			}
		}
	}
	return NOTE_KEPT_FOR_CHEATS
}

// updateOf is the title ID and the version of an update file: from its name, or read from
// the file when the name does not say.
func updateOf(file db.ExtendedFileInfo) (string, int, bool) {
	if match := updateInName.FindStringSubmatch(file.FileName); match != nil {
		version, err := strconv.Atoi(match[2])
		return strings.ToUpper(match[1]), version, err == nil
	}
	path := filepath.Join(file.BaseFolder, file.FileName)
	var contents map[string]*switchfs.ContentMetaAttributes
	var err error
	switch strings.ToLower(filepath.Ext(path)) {
	case ".nsp", ".nsz":
		contents, err = switchfs.ReadNspMetadata(path)
	case ".xci", ".xcz":
		contents, err = switchfs.ReadXciMetadata(path)
	default:
		return "", 0, false
	}
	if err != nil || len(contents) != 1 {
		return "", 0, false
	}
	for id, content := range contents {
		return strings.ToUpper(id), content.Version, true
	}
	return "", 0, false
}
