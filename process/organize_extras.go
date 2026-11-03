package process

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
)

// The files that come with a game without being one, as the NFO and SFV of a release and the
// photos that prove it, go with the game when Organize moves it, instead of staying behind in
// a folder that is then never left empty.

// extraFileTypes are the extensions of the files that go with a game: notes of the release,
// checksums and images.
var extraFileTypes = map[string]bool{".nfo": true, ".diz": true, ".txt": true, ".sfv": true, ".md5": true,
	".sha1": true, ".sha256": true, ".sha512": true, ".crc": true,
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".bmp": true}

func isExtraFile(name string) bool {
	return extraFileTypes[strings.ToLower(filepath.Ext(name))]
}

// isProofFolder tells the folder of the photos that prove a release.
func isProofFolder(name string) bool {
	name = strings.ToLower(name)
	return name == "proof" || name == "proofs"
}

// nameStem is a file name without its extension.
func nameStem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// REASON_EXTRA_NAME_TAKEN explains a file that goes with a game left where it is.
const REASON_EXTRA_NAME_TAKEN = "It goes with a game that was moved, but a file with its name is already where it would go, so it is left where it is."

var (
	errExtraGone  = errors.New("the file is no longer there")
	errExtraTaken = errors.New("its name is taken where it would go")
)

// gameExtras finds the files that go with a game: the ones named as it with another extension
// (game.nfo next to game.nsp), and, when the game has a folder of its own, the other notes,
// checksums and images of that folder, with its Proof folder.
type gameExtras struct {
	// tells a library folder, which is no game's own
	libraryFolder func(folder string) bool
	// how many game files the scan found in each folder, by pathKey
	games map[string]int
	// the folders read, by pathKey, as they were the first time
	folders map[string]*extrasFolder
}

// extrasFolder is what a folder holds for the games in it.
type extrasFolder struct {
	// the files that can go with a game, by their name without extension in lower case
	named map[string][]string
	files []string
	// its Proof folders, and whether it has other folders
	proofs []string
	others bool
}

func newGameExtras(roots []string, localDB *db.LocalSwitchFilesDB) *gameExtras {
	extras := &gameExtras{libraryFolder: libraryFolders(roots), games: map[string]int{}, folders: map[string]*extrasFolder{}}
	counted := map[string]bool{}
	count := func(file db.ExtendedFileInfo) {
		path := pathKey(filepath.Join(file.BaseFolder, file.FileName))
		if file.FileName != "" && !counted[path] && db.IsGameFileType(filepath.Ext(file.FileName)) {
			counted[path] = true
			extras.games[pathKey(file.BaseFolder)]++
		}
	}
	for _, local := range localDB.TitlesMap {
		if local.BaseExist {
			count(local.File.ExtendedInfo)
		}
		for _, update := range local.Updates {
			count(update.ExtendedInfo)
		}
		for _, dlc := range local.Dlc {
			count(dlc.ExtendedInfo)
		}
	}
	for file := range localDB.Skipped {
		count(file)
	}
	return extras
}

// folder is the folder dir as it was the first time it was read: the files the run moves in
// are no game's extras, and the ones it moves out are told by fileOperations.movedFrom. The
// folders created by the run (planned) are not counted among its own.
func (e *gameExtras) folder(dir string, planned map[string]bool) *extrasFolder {
	key := pathKey(dir)
	if folder, ok := e.folders[key]; ok {
		return folder
	}
	folder := &extrasFolder{named: map[string][]string{}}
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case entry.IsDir() && isProofFolder(name):
			folder.proofs = append(folder.proofs, name)
		case entry.IsDir():
			// hidden folders of the system (.AppleDouble, @eaDir of a NAS) hold nothing of the user
			if !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "@") && !planned[filepath.Join(dir, name)] {
				folder.others = true
			}
		case entry.Type().IsRegular() && isExtraFile(name):
			stem := strings.ToLower(nameStem(name))
			folder.named[stem] = append(folder.named[stem], name)
			folder.files = append(folder.files, name)
		}
	}
	e.folders[key] = folder
	return folder
}

// ownFolder tells a folder of the game that is moved from dir to toDir alone: no library
// folder, with no other game and no folders but Proof, which the game leaves.
func (f *fileOperations) ownFolder(dir string, toDir string, folder *extrasFolder) bool {
	if samePath(dir, toDir) || folder.others || f.extras.games[pathKey(dir)] != 1 || f.extras.libraryFolder(dir) {
		return false
	}
	// a game moved to a subfolder of its folder (Base) stays in it, with its files
	rel, err := filepath.Rel(pathKey(dir), pathKey(toDir))
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// moveExtras takes along the files that go with the game moved from from to to (see
// gameExtras): the ones named as the game take its new name, the others of its own folder keep
// theirs.
func (f *fileOperations) moveExtras(from string, to string) {
	dir, toDir := filepath.Dir(from), filepath.Dir(to)
	folder := f.extras.folder(dir, f.plannedDirs)
	toStem := nameStem(filepath.Base(to))
	named := map[string]bool{}
	for _, name := range folder.named[strings.ToLower(nameStem(filepath.Base(from)))] {
		named[name] = true
		f.moveExtra(filepath.Join(dir, name), filepath.Join(toDir, toStem+filepath.Ext(name)))
	}
	if !f.ownFolder(dir, toDir, folder) {
		return
	}
	for _, name := range folder.files {
		if !named[name] {
			f.moveExtra(filepath.Join(dir, name), filepath.Join(toDir, name))
		}
	}
	for _, proofs := range folder.proofs {
		proofFolder := filepath.Join(dir, proofs)
		for _, name := range f.extras.folder(proofFolder, f.plannedDirs).files {
			f.moveExtra(filepath.Join(proofFolder, name), filepath.Join(toDir, proofs, name))
		}
	}
}

// moveExtra moves a file that goes with a game, as move does a game: never over another file.
// One whose name is taken where it would go stays where it is, and is told.
func (f *fileOperations) moveExtra(from string, to string) {
	if from == to {
		return
	}
	if _, moved := f.movedFrom[from]; moved || f.arrived[pathKey(from)] {
		return
	}
	err := func() error {
		fromInfo, err := os.Lstat(from)
		if err != nil || !fromInfo.Mode().IsRegular() {
			return errExtraGone
		}
		if f.arrived[pathKey(to)] {
			return errExtraTaken
		}
		// renaming only the case of a file on a case-insensitive file system is fine
		caseOnly := false
		if toInfo, err := os.Lstat(to); err == nil {
			if !os.SameFile(fromInfo, toInfo) {
				return errExtraTaken
			}
			caseOnly = true
		}
		if err := f.mkdir(filepath.Dir(to)); err != nil {
			return err
		}
		f.plan(to, from)
		if f.dryRun {
			f.movedFrom[from] = to
			return nil
		}
		moveTo := moveFile
		if caseOnly {
			moveTo = os.Rename
		}
		if err := moveTo(from, to); err != nil {
			// a file that came at the destination since it was looked at is never written over
			if _, statErr := os.Lstat(to); errors.Is(err, os.ErrExist) && statErr == nil {
				return errExtraTaken
			}
			return err
		}
		f.movedFrom[from] = to
		return nil
	}()
	switch {
	case errors.Is(err, errExtraGone):
	case errors.Is(err, errExtraTaken):
		f.movedFrom[from] = from
		// told even with a list of files to move, which it is not in: its game is
		f.record(Operation{Kind: OP_SKIP, From: from, Reason: REASON_EXTRA_NAME_TAKEN}, nil)
	default:
		f.record(Operation{Kind: OP_MOVE, From: from, To: to}, err)
	}
}
