package process

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"go.uber.org/zap"
	"robpike.io/nihongo"
)

// Organizing logic based on https://github.com/trembon/switch-library-manager

var (
	folderIllegalCharsRegex = regexp.MustCompile(`[/\\?%*:;=|"<>]`)
	nonAscii                = regexp.MustCompile("[a-zA-Z0-9áéíóú@#%&',.\\s-\\[\\]\\(\\)\\+]")
	cjk                     = regexp.MustCompile("[⽰-⾡぀-ヿ㐀-䶿一-鿿豈-﫿ｦ-ﾟ\\p{Katakana}\\p{Hiragana}\\p{Hangul}]")
	whitespace              = regexp.MustCompile(`\s+`)
)

const (
	OP_MKDIR   = "mkdir"
	OP_MOVE    = "move"
	OP_DELETE  = "delete"
	OP_SKIP    = "skip"
	OP_CLEANUP = "cleanup"
)

// Operation is a single file system change, planned (dry run) or performed.
type Operation struct {
	Kind   string
	From   string
	To     string
	Reason string
	Error  string
}

// fileOperations performs file system changes, or only records them in dry run mode.
// It refuses changes that could lose data, like overwriting an existing file.
type fileOperations struct {
	dryRun         bool
	operations     []Operation
	plannedDirs    map[string]bool
	plannedTargets map[string]string
	// files already moved, by their old path: a file with several contents is listed once
	// for each of them
	movedFrom map[string]string
	logger    *zap.SugaredLogger
}

func newFileOperations(dryRun bool) *fileOperations {
	return &fileOperations{dryRun: dryRun, plannedDirs: map[string]bool{}, plannedTargets: map[string]string{}, movedFrom: map[string]string{}, logger: zap.S()}
}

func (f *fileOperations) record(op Operation, err error) {
	if err != nil {
		op.Error = err.Error()
		f.logger.Errorf("%v %v -> %v failed: %v", op.Kind, op.From, op.To, err)
	} else if !f.dryRun {
		f.logger.Infof("%v %v -> %v", op.Kind, op.From, op.To)
	}
	f.operations = append(f.operations, op)
}

func (f *fileOperations) dirExists(dir string) bool {
	if f.plannedDirs[dir] {
		return true
	}
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func (f *fileOperations) mkdir(dir string) error {
	dir = filepath.Clean(dir)
	if f.dirExists(dir) {
		return nil
	}
	var err error
	if !f.dryRun {
		err = os.MkdirAll(dir, os.ModePerm)
	}
	f.plannedDirs[dir] = true
	f.record(Operation{Kind: OP_MKDIR, To: dir}, err)
	return err
}

func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// REASON_SPLIT explains a part of a split file left where it is.
const REASON_SPLIT = "split files are not organized"

// isSplitPart tells the parts of a split file ("00", "01"...): the first one stands for the
// whole file in the library, and moving or deleting it alone would leave the others behind.
func isSplitPart(name string) bool {
	if len(name) < 2 {
		return false
	}
	_, err := strconv.Atoi(name[len(name)-2:])
	return err == nil
}

func (f *fileOperations) move(from string, to string) error {
	from, to = filepath.Clean(from), filepath.Clean(to)
	if from != to && isSplitPart(filepath.Base(from)) {
		if _, moved := f.movedFrom[from]; !moved {
			f.movedFrom[from] = from
			f.skip(from, REASON_SPLIT)
		}
		return nil
	}
	if _, moved := f.movedFrom[from]; moved {
		// moved already with another of its contents, or in place with it
		return nil
	}
	if from == to {
		// in place: its other contents must not move it to their own names
		f.movedFrom[from] = to
		return nil
	}

	err := func() error {
		fromInfo, err := os.Stat(from)
		if err != nil {
			return fmt.Errorf("source not found, rescan the library: %w", err)
		}
		for target, source := range f.plannedTargets {
			if samePath(target, to) && source != from {
				return errors.New("another file is moved to the same destination")
			}
		}
		// renaming only the case of a file on a case-insensitive file system is fine
		if toInfo, err := os.Stat(to); err == nil && !os.SameFile(fromInfo, toInfo) {
			if toInfo.Size() == fromInfo.Size() {
				return errDuplicateAtDestination
			}
			return errors.New("another file with the same name is already at the destination")
		}
		if err := f.mkdir(filepath.Dir(to)); err != nil {
			return err
		}
		f.plannedTargets[to] = from
		if f.dryRun {
			f.movedFrom[from] = to
			return nil
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
		f.movedFrom[from] = to
		return nil
	}()

	if errors.Is(err, errDuplicateAtDestination) {
		// left where it is: Space and the cleanup of duplicates take care of it
		f.skip(from, REASON_DUPLICATE_AT_DESTINATION)
		return nil
	}
	f.record(Operation{Kind: OP_MOVE, From: from, To: to}, err)
	return err
}

// errDuplicateAtDestination is a move to a file of the same size: a copy of the same game.
var errDuplicateAtDestination = errors.New("a copy is already at the destination")

// REASON_DUPLICATE_AT_DESTINATION explains a file left where it is because a copy of it is
// already where it would go.
const REASON_DUPLICATE_AT_DESTINATION = "A copy of the same size is already at the destination: it is a duplicate, the Space page can free it."

// remove deletes a file found by the last scan, unless it changed since then.
func (f *fileOperations) remove(file db.ExtendedFileInfo, reason string) error {
	filePath := filepath.Join(file.BaseFolder, file.FileName)
	if isSplitPart(file.FileName) {
		f.skip(filePath, REASON_SPLIT)
		return nil
	}
	err := func() error {
		info, err := os.Stat(filePath)
		if err != nil {
			return fmt.Errorf("file not found, rescan the library: %w", err)
		}
		if info.IsDir() || info.Size() != file.Size {
			return errors.New("file changed since the last scan, rescan the library")
		}
		if f.dryRun {
			return nil
		}
		return os.Remove(filePath)
	}()

	f.record(Operation{Kind: OP_DELETE, From: filePath, Reason: reason}, err)
	return err
}

func (f *fileOperations) skip(file string, reason string) {
	f.record(Operation{Kind: OP_SKIP, From: file, Reason: reason}, nil)
}

func (f *fileOperations) deleteEmptyFolders(root string) {
	if f.dryRun {
		f.record(Operation{Kind: OP_CLEANUP, From: root, Reason: "empty folders will be deleted"}, nil)
		return
	}

	// deepest folders first, so parents that become empty are deleted as well
	folders := []string{}
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() && p != root {
			folders = append(folders, p)
		}
		return nil
	})
	sort.Slice(folders, func(i, j int) bool { return len(folders[i]) > len(folders[j]) })

	for _, folder := range folders {
		entries, err := os.ReadDir(folder)
		if err != nil || len(entries) != 0 {
			continue
		}
		f.record(Operation{Kind: OP_DELETE, From: folder, Reason: "empty folder"}, os.Remove(folder))
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// REASON_IN_USE is why a file listed as old or duplicate is kept: a file with several
// contents (a complete pack, a card with its update) is listed for one of them.
const REASON_IN_USE = "It also holds the game, its latest update or a DLC that the library keeps, so it is not deleted."

// FilesInUse are the paths of the files that hold content the library keeps: the game,
// its latest update and every DLC. Such a file is never deleted as old or duplicate.
func FilesInUse(localDB *db.LocalSwitchFilesDB) map[string]bool {
	inUse := map[string]bool{}
	if localDB == nil {
		return inUse
	}
	add := func(file db.ExtendedFileInfo) {
		inUse[filepath.Join(file.BaseFolder, file.FileName)] = true
	}
	for _, title := range localDB.TitlesMap {
		if title.BaseExist {
			add(title.File.ExtendedInfo)
		}
		if update, ok := title.Updates[title.LatestUpdate]; ok {
			add(update.ExtendedInfo)
		}
		for _, dlc := range title.Dlc {
			add(dlc.ExtendedInfo)
		}
	}
	return inUse
}

// DeleteOldUpdates deletes update and DLC files superseded by a newer local version and,
// if requested, duplicate files. The scan keeps the first file of a duplicate set. A file
// that still holds content the library keeps is never deleted, and keep can refuse others
// with the reason it returns (the file that stays was not checked, or is damaged).
func DeleteOldUpdates(baseFolder string, localDB *db.LocalSwitchFilesDB, options settings.OrganizeOptions,
	dryRun bool, updateProgress db.ProgressUpdater, keep ...func(db.ExtendedFileInfo, db.SkippedFile) string) []Operation {

	ops := newFileOperations(dryRun)
	inUse := FilesInUse(localDB)

	files := make([]db.ExtendedFileInfo, 0, len(localDB.Skipped))
	for file := range localDB.Skipped {
		files = append(files, file)
	}
	sort.Slice(files, func(i, j int) bool {
		return filepath.Join(files[i].BaseFolder, files[i].FileName) < filepath.Join(files[j].BaseFolder, files[j].FileName)
	})

	for i, file := range files {
		skipped := localDB.Skipped[file]
		if skipped.ReasonCode != db.REASON_OLD_UPDATE && !(options.DeleteDuplicateFiles && skipped.ReasonCode == db.REASON_DUPLICATE) {
			continue
		}
		if updateProgress != nil {
			updateProgress.UpdateProgress(i+1, len(files), file.FileName)
		}
		if inUse[filepath.Join(file.BaseFolder, file.FileName)] {
			// an old update in a file that holds the game is the game: not listed every time
			if skipped.ReasonCode != db.REASON_OLD_UPDATE {
				ops.skip(filepath.Join(file.BaseFolder, file.FileName), REASON_IN_USE)
			}
			continue
		}
		refused := ""
		for _, check := range keep {
			if refused == "" {
				refused = check(file, skipped)
			}
		}
		if refused != "" {
			ops.skip(filepath.Join(file.BaseFolder, file.FileName), refused)
			continue
		}
		ops.remove(file, skipped.ReasonText)
	}

	if options.DeleteEmptyFolders && baseFolder != "" {
		ops.deleteEmptyFolders(baseFolder)
	}

	return ops.operations
}

// resolveFolder returns folder relative to baseFolder unless it is absolute.
func resolveFolder(baseFolder string, folder string) string {
	if filepath.IsAbs(folder) {
		return folder
	}
	return filepath.Join(baseFolder, folder)
}

// OrganizeByFolders moves (and optionally renames) the library files according to the
// organize options. With dryRun the returned operations are only planned.
func OrganizeByFolders(baseFolder string,
	localDB *db.LocalSwitchFilesDB,
	titlesDB *db.SwitchTitlesDB,
	options settings.OrganizeOptions,
	dryRun bool,
	updateProgress db.ProgressUpdater) ([]Operation, error) {

	if err := ValidateOptions(options); err != nil {
		return nil, err
	}
	if baseFolder == "" && options.CreateFolderPerGame {
		return nil, errors.New("no library folder configured")
	}

	ops := newFileOperations(dryRun)
	keys := sortedKeys(localDB.TitlesMap)
	tasksSize := len(keys) + 1

	for i, k := range keys {
		v := localDB.TitlesMap[k]
		if !v.BaseExist && !options.ProcessWhenMissingBaseGame {
			continue
		}

		var title *db.SwitchTitle
		if titlesDB != nil {
			title = titlesDB.TitlesMap[k]
		}
		titleName := getTitleName(title, v)
		// a name in Chinese or Korean is left with no letter by the names for the Switch: the
		// file's own name, or Unknown Title, instead of a folder called "_"
		if options.SwitchSafeFileNames && strings.Trim(safeName(titleName), " -_.") == "" {
			titleName = asciiFallbackName(v)
		}
		if updateProgress != nil {
			updateProgress.UpdateProgress(i+1, tasksSize, titleName)
		}

		templateData := map[string]string{}

		if title != nil && title.Attributes.Id != "" {
			templateData[settings.TEMPLATE_TITLE_ID] = title.Attributes.Id
		} else if v.File.Metadata != nil {
			templateData[settings.TEMPLATE_TITLE_ID] = v.File.Metadata.TitleId
		}

		templateData[settings.TEMPLATE_TITLE_NAME] = titleName
		templateData[settings.TEMPLATE_VERSION_TXT] = ""

		if title != nil {
			templateData[settings.TEMPLATE_REGION] = title.Attributes.Region
		}

		// a multi content file is named with the latest update it holds itself, not with a
		// newer one in a file of its own
		latestUpdate := 0
		if v.MultiContent {
			for update, info := range v.Updates {
				if info.ExtendedInfo == v.File.ExtendedInfo && update > latestUpdate {
					latestUpdate = update
				}
			}
		}
		if latestUpdate > 0 {
			templateData[settings.TEMPLATE_VERSION] = strconv.Itoa(latestUpdate)

			if latestUpdate > 0 && v.Updates[latestUpdate].Metadata != nil && v.Updates[latestUpdate].Metadata.Ncap != nil {
				templateData[settings.TEMPLATE_VERSION_TXT] = v.Updates[latestUpdate].Metadata.Ncap.DisplayVersion
			}
		} else {
			templateData[settings.TEMPLATE_VERSION] = "0"

			if v.File.Metadata != nil && v.File.Metadata.Ncap != nil {
				templateData[settings.TEMPLATE_VERSION_TXT] = v.File.Metadata.Ncap.DisplayVersion
			}
		}

		if v.IsSplit {
			// the parts of a split file must stay together in their folder
			ops.skip(filepath.Join(v.File.ExtendedInfo.BaseFolder, v.File.ExtendedInfo.FileName), REASON_SPLIT)
			continue
		}

		destinationPath := v.File.ExtendedInfo.BaseFolder
		if !v.BaseExist {
			destinationPath = ""
		}

		// the folder of the game is created when a file moves into it: a file of several
		// games stays in the folder of the first one, and an empty folder would be created and
		// deleted again on every run
		if options.CreateFolderPerGame {
			destinationPath = filepath.Join(baseFolder, getFolderName(options, templateData))
		}

		//process base title
		if v.BaseExist {
			templateData[settings.TEMPLATE_TYPE] = "BASE"
			from := filepath.Join(v.File.ExtendedInfo.BaseFolder, v.File.ExtendedInfo.FileName)
			to := filepath.Join(destinationPath, getFileName(options, v.File.ExtendedInfo.FileName, templateData, 0))
			if err := ops.move(from, to); err != nil {
				continue
			}
		}

		// destination folder of an update or DLC file
		targetFolder := func(file db.ExtendedFileInfo, subFolder string) string {
			if options.CreateFolderPerGame {
				if subFolder != "" {
					return filepath.Join(destinationPath, subFolder)
				}
				return destinationPath
			}
			if subFolder != "" {
				return resolveFolder(baseFolder, subFolder)
			}
			return file.BaseFolder
		}

		//process updates
		for _, update := range sortedIntKeys(v.Updates) {
			updateInfo := v.Updates[update]
			// if the current title is multi content and the update is contained in the main file, skip
			if v.MultiContent && v.BaseExist && v.File.ExtendedInfo == updateInfo.ExtendedInfo {
				continue
			}

			if updateInfo.Metadata != nil {
				templateData[settings.TEMPLATE_TITLE_ID] = updateInfo.Metadata.TitleId
			}
			templateData[settings.TEMPLATE_VERSION] = strconv.Itoa(update)
			templateData[settings.TEMPLATE_TYPE] = "UPD"
			if updateInfo.Metadata != nil && updateInfo.Metadata.Ncap != nil {
				templateData[settings.TEMPLATE_VERSION_TXT] = updateInfo.Metadata.Ncap.DisplayVersion
			} else {
				templateData[settings.TEMPLATE_VERSION_TXT] = ""
			}

			from := filepath.Join(updateInfo.ExtendedInfo.BaseFolder, updateInfo.ExtendedInfo.FileName)
			to := filepath.Join(targetFolder(updateInfo.ExtendedInfo, options.UpdatesFolder), getFileName(options, updateInfo.ExtendedInfo.FileName, templateData, 0))
			ops.move(from, to)
		}

		//process DLC
		existingDlcs := map[string]string{}
		for _, id := range sortedKeys(v.Dlc) {
			dlc := v.Dlc[id]
			// if the current title is multi content and the dlc is contained in the main file, skip
			if v.MultiContent && v.BaseExist && v.File.ExtendedInfo == dlc.ExtendedInfo {
				continue
			}

			templateData[settings.TEMPLATE_VERSION] = "0"
			templateData[settings.TEMPLATE_VERSION_TXT] = ""
			if dlc.Metadata != nil {
				templateData[settings.TEMPLATE_VERSION] = strconv.Itoa(dlc.Metadata.Version)
			}
			templateData[settings.TEMPLATE_TYPE] = "DLC"
			templateData[settings.TEMPLATE_TITLE_ID] = id
			templateData[settings.TEMPLATE_DLC_NAME] = getDlcName(title, dlc)
			from := filepath.Join(dlc.ExtendedInfo.BaseFolder, dlc.ExtendedInfo.FileName)
			folder := targetFolder(dlc.ExtendedInfo, options.DlcFolder)

			// DLC with different IDs can share a name, number them instead of colliding
			var to string
			for dlcNameTry := 0; ; dlcNameTry++ {
				to = filepath.Join(folder, getFileName(options, dlc.ExtendedInfo.FileName, templateData, dlcNameTry))
				if existingId, exists := existingDlcs[to]; !exists || existingId == id {
					break
				}
			}
			existingDlcs[to] = id

			ops.move(from, to)
		}
	}

	if options.DeleteEmptyFolders && baseFolder != "" {
		if updateProgress != nil {
			updateProgress.UpdateProgress(tasksSize, tasksSize, "Deleting empty folders...")
		}
		ops.deleteEmptyFolders(baseFolder)
	}

	return ops.operations, nil
}

func sortedIntKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// ValidateOptions checks that the templates produce identifiable file and folder names.
func ValidateOptions(options settings.OrganizeOptions) error {
	if options.RenameFiles {
		if options.FileNameTemplate == "" {
			return errors.New("file name template cannot be empty")
		}
		if !strings.Contains(options.FileNameTemplate, settings.TEMPLATE_TITLE_NAME) &&
			!strings.Contains(options.FileNameTemplate, settings.TEMPLATE_TITLE_ID) {
			return errors.New("file name template needs to contain {TITLE_NAME} or {TITLE_ID}")
		}
	}

	if options.CreateFolderPerGame {
		if options.FolderNameTemplate == "" {
			return errors.New("folder name template cannot be empty")
		}
		if !strings.Contains(options.FolderNameTemplate, settings.TEMPLATE_TITLE_NAME) &&
			!strings.Contains(options.FolderNameTemplate, settings.TEMPLATE_TITLE_ID) {
			return errors.New("folder name template needs to contain {TITLE_NAME} or {TITLE_ID}")
		}
	}
	return nil
}

func getDlcName(switchTitle *db.SwitchTitle, file db.SwitchFileInfo) string {
	if switchTitle == nil || file.Metadata == nil {
		return ""
	}
	if dlcAttributes, ok := switchTitle.Dlc[file.Metadata.TitleId]; ok {
		name := dlcAttributes.Name
		name = strings.ReplaceAll(name, "\n", " ")
		return name
	}
	return ""
}

// the languages of the names a game gives itself, in the order they are preferred for its
// folder and file names
var (
	latinTitleLanguages = []string{"AmericanEnglish", "BritishEnglish", "Spanish", "LatinAmericanSpanish", "French",
		"CanadianFrench", "German", "Italian", "Dutch", "Portuguese", "Russian"}
	asianTitleLanguages = []string{"Japanese", "Korean", "Chinese", "Taiwanese"}
)

// realName tells a name from a placeholder such as "-" of some entries of the titles database.
func realName(name string) bool {
	return strings.Trim(name, " -_.?·") != ""
}

func getTitleName(switchTitle *db.SwitchTitle, v *db.SwitchGameFiles) string {
	if switchTitle != nil && !realName(switchTitle.Attributes.Name) {
		switchTitle = nil
	}
	if switchTitle != nil && switchTitle.Attributes.Name != "" {
		res := cjk.FindAllString(switchTitle.Attributes.Name, -1)
		if len(res) == 0 {
			return switchTitle.Attributes.Name
		}
	}

	// the name the game gives itself: in English first, then in another language written in
	// Latin letters, then the name of the titles database in Asian characters, then the
	// game's own in Japanese, Korean or Chinese, before the name of the file
	var nacp map[string]switchfs.NacpTitle
	if v != nil && v.File.Metadata != nil && v.File.Metadata.Ncap != nil {
		nacp = v.File.Metadata.Ncap.TitleName
	}
	for _, language := range latinTitleLanguages {
		if name := strings.TrimSpace(nacp[language].Title); realName(name) {
			return name
		}
	}
	if switchTitle != nil && strings.TrimSpace(switchTitle.Attributes.Name) != "" {
		return switchTitle.Attributes.Name
	}
	for _, language := range asianTitleLanguages {
		if name := strings.TrimSpace(nacp[language].Title); realName(name) {
			return name
		}
	}

	//for non eshop games (cartridge only), grab the name from the file
	if v != nil && v.File.ExtendedInfo.FileName != "" {
		name := db.ParseTitleNameFromFileName(v.File.ExtendedInfo.FileName)
		// a name without [TitleID] is the whole file name: without its extension, or the
		// folder of the game would be named like the file and could not be made next to it
		if name == v.File.ExtendedInfo.FileName {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}

	return "Unknown Title"
}

// maxNameBytes is the longest file or folder name written: Linux file systems take 255
// bytes, and a hidden temporary copy (".name.tmp") needs a few more.
const maxNameBytes = 240

func getFolderName(options settings.OrganizeOptions, templateData map[string]string) string {
	return fittingTemplate(templateData, options.SwitchSafeFileNames, options.FolderNameTemplate, 0, maxNameBytes)
}

func getFileName(options settings.OrganizeOptions, originalName string, templateData map[string]string, nameTry int) string {
	if !options.RenameFiles {
		return originalName
	}
	ext := path.Ext(originalName)
	result := fittingTemplate(templateData, options.SwitchSafeFileNames, options.FileNameTemplate, nameTry, maxNameBytes-len(ext))
	return result + ext
}

// fittingTemplate is applyTemplate shortened to budget bytes: the names of the DLC and of
// the game are cut, on a character boundary, never the title ID or the version that
// identify the file.
func fittingTemplate(templateData map[string]string, useSafeNames bool, template string, nameTry int, budget int) string {
	result := applyTemplate(templateData, useSafeNames, template, nameTry)
	if len(result) <= budget {
		return result
	}
	data := make(map[string]string, len(templateData))
	for key, value := range templateData {
		data[key] = value
	}
	for _, key := range []string{settings.TEMPLATE_DLC_NAME, settings.TEMPLATE_TITLE_NAME} {
		for len(result) > budget {
			runes := []rune(data[key])
			if len(runes) == 0 {
				break
			}
			// a few characters at a time: names made safe change length
			cut := (len(result) - budget + 2) / 3
			if cut < 1 {
				cut = 1
			}
			if cut > len(runes) {
				cut = len(runes)
			}
			data[key] = strings.TrimSpace(string(runes[:len(runes)-cut]))
			result = applyTemplate(data, useSafeNames, template, nameTry)
		}
	}
	return result
}

// windowsReserved are names Windows does not allow, with or without an extension.
var windowsReserved = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// avoidReserved adds an underscore to a name Windows does not allow (such as "Con").
func avoidReserved(name string) string {
	stem, rest, _ := strings.Cut(name, ".")
	if windowsReserved[strings.ToUpper(strings.TrimSpace(stem))] {
		if rest == "" && !strings.Contains(name, ".") {
			return stem + "_"
		}
		return stem + "_." + rest
	}
	return name
}

func applyTemplate(templateData map[string]string, useSafeNames bool, template string, nameTry int) string {
	result := strings.ReplaceAll(template, "{"+settings.TEMPLATE_TITLE_NAME+"}", templateData[settings.TEMPLATE_TITLE_NAME])
	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_TITLE_ID+"}", strings.ToUpper(templateData[settings.TEMPLATE_TITLE_ID]))
	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_VERSION+"}", templateData[settings.TEMPLATE_VERSION])
	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_TYPE+"}", templateData[settings.TEMPLATE_TYPE])
	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_VERSION_TXT+"}", templateData[settings.TEMPLATE_VERSION_TXT])
	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_REGION+"}", templateData[settings.TEMPLATE_REGION])

	//remove title name from dlc name
	dlcName := strings.Replace(templateData[settings.TEMPLATE_DLC_NAME], templateData[settings.TEMPLATE_TITLE_NAME], "", 1)
	dlcName = strings.TrimSpace(dlcName)
	dlcName = strings.TrimPrefix(dlcName, "-")
	dlcName = strings.TrimSpace(dlcName)

	result = strings.ReplaceAll(result, "{"+settings.TEMPLATE_DLC_NAME+"}", dlcName)
	result = strings.ReplaceAll(result, "[]", "")
	result = strings.ReplaceAll(result, "()", "")
	result = strings.ReplaceAll(result, "<>", "")

	result = strings.TrimSuffix(result, ".")

	if nameTry > 0 {
		result = result + "(" + strconv.Itoa(nameTry) + ")"
	}

	if useSafeNames {
		result = nihongo.RomajiString(result)

		// handle known characters that have safe variants
		result = strings.ReplaceAll(result, "ō", "o")

		safe := nonAscii.FindAllString(result, -1)
		result = strings.Join(safe, "")
	}

	result = whitespace.ReplaceAllString(result, " ")

	result = strings.TrimSpace(result)
	result = folderIllegalCharsRegex.ReplaceAllString(result, "")
	// names such as ".." or "." would point outside the folder of the game
	result = strings.Trim(result, ". ")
	if result == "" {
		result = "_"
	}
	return avoidReserved(result)
}

// safeName is a name as the names for the Switch write it: in romaji and in ASCII only.
func safeName(name string) string {
	name = strings.ReplaceAll(nihongo.RomajiString(name), "ō", "o")
	return strings.Join(nonAscii.FindAllString(name, -1), "")
}

// asciiFallbackName is the name of a game whose name has no letter for the Switch: the one in
// its file name when it has some, else Unknown Title.
func asciiFallbackName(v *db.SwitchGameFiles) string {
	if v != nil && v.File.ExtendedInfo.FileName != "" {
		name := db.ParseTitleNameFromFileName(v.File.ExtendedInfo.FileName)
		if name == v.File.ExtendedInfo.FileName {
			name = strings.TrimSuffix(name, filepath.Ext(name))
		}
		if name = strings.TrimSpace(safeName(name)); strings.Trim(name, " -_.") != "" {
			return name
		}
	}
	return "Unknown Title"
}

// RemoveFiles deletes the given files, each with its reason, refusing files that changed
// since the last scan. With dryRun the deletions are only planned.
func RemoveFiles(files []db.ExtendedFileInfo, reasons []string, dryRun bool) []Operation {
	ops := newFileOperations(dryRun)
	for i, file := range files {
		ops.remove(file, reasons[i])
	}
	return ops.operations
}
