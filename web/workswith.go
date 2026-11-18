package web

import (
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Which installers take a file of the library and whether emulators open it as it is, by how
// the file keeps its NCA files (switchfs.FileFacts.Storage), as NXPress tells it in Details: an
// NSP or an XCI works with everything; a solid NSZ with every installer (NXDock, DBI, Tinfoil,
// Awoo, CyberFoil, Sphaira), and emulators need it decompressed; an NSZ in blocks or an XCZ with
// every installer but Awoo, and the emulators that read NSZ run it as it is; the extra
// compression of NXPress (NCZSECT2 and NCZSECT3) only with NXDock; a game merged with its
// update, optimized for install or by another tool, with NXDock or DBI, with sigpatches. A NUP
// tells what Eglantine and Lichii make of it on its own (see nupEmulators).

// what the page of a game says of the installers and the emulators of a file
const (
	installersAll        = "Installers: all"
	installersButAwoo    = "Installers: all but Awoo"
	installersOnlyNXDock = "Installers: only NXDock"
	installersSigpatches = "Installers: NXDock or DBI, with sigpatches"
	emulatorsAsItIs      = "Emulators: as it is"
	emulatorsDecompress  = "Emulators: decompress it first"
	emulatorsNo          = "Emulators: no"
	worksUnknown         = "Installers and emulators: unknown"
)

// the case of a file, told on hover
const (
	worksUnknownHelp     = "How the file is stored could not be read: the file may be missing, or it is damaged."
	worksPlainHelp       = "Not compressed: every installer (NXDock, DBI, Tinfoil, Awoo, CyberFoil, Sphaira) installs it, and emulators open it as it is."
	worksSolidHelp       = "Compressed in one piece (solid): every installer (NXDock, DBI, Tinfoil, Awoo, CyberFoil, Sphaira) installs it; emulators need it decompressed first."
	worksBlocksHelp      = "Compressed in blocks: every installer but Awoo installs it, and the emulators that read NSZ and XCZ open it as it is."
	worksExtraHelp       = "Made with the extra compression of NXPress. NXDock installs it as it is; for DBI, Tinfoil and the emulators, decompress it first with NXPress."
	worksMergedHelp      = "The game merged with its update (optimized for install, or by another tool): install it with NXDock or DBI, with sigpatches. Emulators open it as it is."
	worksMergedSolidHelp = "The game merged with its update (optimized for install, or by another tool): install it with NXDock or DBI, with sigpatches. Emulators need it decompressed first."
)

// FileWorks is what a file of the game page works with, for its row in Files: which installers
// take it and whether emulators open it as it is, each green when everything does and orange
// when there is a limit (never red: the file is whole), and the case explained on hover. The
// texts are keys of the translations.
type FileWorks struct {
	Installers   string
	InstallersOk bool
	Emulators    string
	EmulatorsOk  bool
	Help         string
	// how the file is stored could not be read: one line says so, in grey
	Unknown bool
	// how the file keeps its NCA files, and whether it is the game merged with its update
	storage string
	merged  bool
}

// worksWith is what a file stored so works with (storage is a switchfs.Storage*, "" when it is
// not known); merged tells it is the game merged with its update.
func worksWith(storage string, merged bool) *FileWorks {
	works := &FileWorks{storage: storage, merged: merged}
	switch storage {
	case switchfs.StorageArchive, switchfs.StorageSparse:
		works.Installers, works.Emulators, works.Help = installersOnlyNXDock, emulatorsNo, worksExtraHelp
	case switchfs.StoragePlain, switchfs.StorageBlocks, switchfs.StorageSolid:
		works.Emulators, works.EmulatorsOk = emulatorsAsItIs, true
		if storage == switchfs.StorageSolid {
			works.Emulators, works.EmulatorsOk = emulatorsDecompress, false
		}
		switch {
		case merged && storage == switchfs.StorageSolid:
			works.Installers, works.Help = installersSigpatches, worksMergedSolidHelp
		case merged:
			works.Installers, works.Help = installersSigpatches, worksMergedHelp
		case storage == switchfs.StorageSolid:
			works.Installers, works.InstallersOk, works.Help = installersAll, true, worksSolidHelp
		case storage == switchfs.StorageBlocks:
			works.Installers, works.Help = installersButAwoo, worksBlocksHelp
		default:
			works.Installers, works.InstallersOk, works.Help = installersAll, true, worksPlainHelp
		}
	default:
		works.Installers, works.Help, works.Unknown = worksUnknown, worksUnknownHelp, true
	}
	return works
}

// fileWorks is what a file of the library works with, from what the library noted when it read
// it; nil for a NUP, whose row tells what Eglantine and Lichii make of it.
func fileWorks(info db.SwitchFileInfo, merged bool) *FileWorks {
	if switchfs.IsNupFile(info.ExtendedInfo.FileName) {
		return nil
	}
	storage := ""
	if info.Metadata != nil {
		storage = info.Metadata.Storage
	}
	return worksWith(storage, merged)
}

// readUnknownStorage reads how the files of the page are stored when the library did not note it
// (data cached before, or a file it named only): a few bytes of each, for the page shown only.
// In the demo the files are made up, and left as they are.
func readUnknownStorage(detail *TitleDetail) {
	if isDemoMode() {
		return
	}
	read := func(file *TitleFile) {
		if file == nil || file.Works == nil || !file.Works.Unknown {
			return
		}
		if storage, err := switchfs.ReadStorage(file.Path); err == nil {
			file.Works = worksWith(storage, file.Works.merged)
		}
	}
	read(detail.Base)
	for i := range detail.Updates {
		read(&detail.Updates[i])
	}
	for i := range detail.Dlc {
		read(detail.Dlc[i].File)
	}
}

// the games of the Library by their emulators: the files the library keeps of the game (the
// game, its newest update and its DLC) all open as they are, or one must be decompressed first
const (
	EMULATORS_AS_IS     = "as_is"
	EMULATORS_NOT_AS_IS = "not_as_is"
)

// gameEmulators tells whether emulators open as they are all the files the library keeps of a
// game: its own, its newest update and its DLC. It is EMULATORS_NOT_AS_IS when one of them must be
// decompressed first, and "" when how one of the others is stored is not known. A NUP opens as it
// is when Eglantine and Lichii read it.
func gameEmulators(local *db.SwitchGameFiles) string {
	if local == nil || !local.BaseExist {
		return ""
	}
	files := []db.SwitchFileInfo{local.File}
	if update, ok := local.Updates[local.LatestUpdate]; ok {
		files = append(files, update)
	}
	for _, dlc := range local.Dlc {
		files = append(files, dlc)
	}
	known := true
	for _, file := range files {
		switch fileOpensAsItIs(file) {
		case EMULATORS_NOT_AS_IS:
			return EMULATORS_NOT_AS_IS
		case "":
			known = false
		}
	}
	if !known {
		return ""
	}
	return EMULATORS_AS_IS
}

// fileOpensAsItIs tells whether emulators open a file as it is (EMULATORS_AS_IS), need it
// decompressed (EMULATORS_NOT_AS_IS), or "" when it is not known.
func fileOpensAsItIs(file db.SwitchFileInfo) string {
	if file.Metadata == nil {
		return ""
	}
	if switchfs.IsNupFile(file.ExtendedInfo.FileName) {
		switch {
		case file.Metadata.Emulators == nil:
			return ""
		case file.Metadata.Emulators.Ok():
			return EMULATORS_AS_IS
		}
		return EMULATORS_NOT_AS_IS
	}
	works := worksWith(file.Metadata.Storage, false)
	switch {
	case works.Unknown:
		return ""
	case works.EmulatorsOk:
		return EMULATORS_AS_IS
	}
	return EMULATORS_NOT_AS_IS
}

// cleanEmulators is the emulators filter as the Library takes it: one of its choices, else "".
func cleanEmulators(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value != EMULATORS_AS_IS && value != EMULATORS_NOT_AS_IS {
		return ""
	}
	return value
}
