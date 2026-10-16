package web

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// compressedTwin is the uncompressed type a compressed copy replaces.
var compressedTwin = map[string]string{".nsz": ".nsp", ".xcz": ".xci"}

// preferCompressed gives the library the cleanup works on: when a duplicate is a compressed
// copy (NSZ, XCZ) of the file the library keeps, checked and sound and holding the same
// contents, the compressed copy stays and the uncompressed file goes instead, whichever the
// scan found first. The library itself is not changed.
func (web *Web) preferCompressed(localDB *db.LocalSwitchFilesDB) *db.LocalSwitchFilesDB {
	swaps := map[db.ExtendedFileInfo]db.ExtendedFileInfo{}
	reasons := map[db.ExtendedFileInfo]string{}
	for file, skipped := range localDB.Skipped {
		if skipped.ReasonCode != db.REASON_DUPLICATE {
			continue
		}
		twin, ok := compressedTwin[strings.ToLower(filepath.Ext(file.FileName))]
		kept := keptBy(skipped.ReasonText)
		if !ok || kept == "" || strings.ToLower(filepath.Ext(kept)) != twin {
			continue
		}
		compressedPath := filepath.Join(file.BaseFolder, file.FileName)
		info, err := os.Stat(compressedPath)
		if err != nil || info.IsDir() {
			continue
		}
		if record, ok := web.verifications().current(compressedPath, info); !ok || !record.sound() {
			continue
		}
		keptInfo, err := os.Stat(kept)
		if err != nil || keptInfo.IsDir() || !web.holdsSameContents(kept, compressedPath, info) {
			continue
		}
		original := db.ExtendedFileInfo{FileName: filepath.Base(kept), BaseFolder: filepath.Dir(kept), Size: keptInfo.Size()}
		swaps[original] = file
		prefix := skipped.ReasonText
		if i := strings.LastIndex(prefix, " ("); i >= 0 {
			prefix = prefix[:i]
		}
		reasons[original] = prefix + " (" + compressedPath + ")"
	}
	if len(swaps) == 0 {
		return localDB
	}

	// the library keeps the compressed copies: what is in use follows them
	byPath := map[string]db.ExtendedFileInfo{}
	for original, compressed := range swaps {
		byPath[filepath.Join(original.BaseFolder, original.FileName)] = compressed
	}
	swap := func(info db.ExtendedFileInfo) (db.ExtendedFileInfo, bool) {
		compressed, ok := byPath[filepath.Join(info.BaseFolder, info.FileName)]
		return compressed, ok
	}
	result := *localDB
	result.TitlesMap = make(map[string]*db.SwitchGameFiles, len(localDB.TitlesMap))
	for id, title := range localDB.TitlesMap {
		changed := false
		copied := *title
		if compressed, ok := swap(title.File.ExtendedInfo); ok {
			copied.File.ExtendedInfo, changed = compressed, true
		}
		copied.Updates = map[int]db.SwitchFileInfo{}
		for version, update := range title.Updates {
			if compressed, ok := swap(update.ExtendedInfo); ok {
				update.ExtendedInfo, changed = compressed, true
			}
			copied.Updates[version] = update
		}
		copied.Dlc = map[string]db.SwitchFileInfo{}
		for dlcId, dlc := range title.Dlc {
			if compressed, ok := swap(dlc.ExtendedInfo); ok {
				dlc.ExtendedInfo, changed = compressed, true
			}
			copied.Dlc[dlcId] = dlc
		}
		if changed {
			result.TitlesMap[id] = &copied
		} else {
			result.TitlesMap[id] = title
		}
	}
	result.Skipped = make(map[db.ExtendedFileInfo]db.SkippedFile, len(localDB.Skipped))
	for file, skipped := range localDB.Skipped {
		result.Skipped[file] = skipped
	}
	for original, compressed := range swaps {
		skipped := result.Skipped[compressed]
		delete(result.Skipped, compressed)
		skipped.ReasonText = reasons[original]
		result.Skipped[original] = skipped
	}
	return &result
}
