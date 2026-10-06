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
	logger         *zap.SugaredLogger
}

func newFileOperations(dryRun bool) *fileOperations {
	return &fileOperations{dryRun: dryRun, plannedDirs: map[string]bool{}, plannedTargets: map[string]string{}, logger: zap.S()}
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

func (f *fileOperations) move(from string, to string) error {
	from, to = filepath.Clean(from), filepath.Clean(to)
	if from == to {
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
			return errors.New("destination already exists")
		}
		if err := f.mkdir(filepath.Dir(to)); err != nil {
			return err
		}
		f.plannedTargets[to] = from
		if f.dryRun {
			return nil
		}
		return os.Rename(from, to)
	}()

	f.record(Operation{Kind: OP_MOVE, From: from, To: to}, err)
	return err
}

// remove deletes a file found by the last scan, unless it changed since then.
func (f *fileOperations) remove(file db.ExtendedFileInfo, reason string) error {
	filePath := filepath.Join(file.BaseFolder, file.FileName)
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

// DeleteOldUpdates deletes update and DLC files superseded by a newer local version and,
// if requested, duplicate files. The scan keeps the first file of a duplicate set.
func DeleteOldUpdates(baseFolder string, localDB *db.LocalSwitchFilesDB, options settings.OrganizeOptions,
	dryRun bool, updateProgress db.ProgressUpdater) []Operation {

	ops := newFileOperations(dryRun)

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
			updateProgress.UpdateProgress(i+1, len(files), "deleting "+file.FileName)
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

		if updateProgress != nil {
			updateProgress.UpdateProgress(i+1, tasksSize, k)
		}

		var title *db.SwitchTitle
		if titlesDB != nil {
			title = titlesDB.TitlesMap[k]
		}
		titleName := getTitleName(title, v)

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

		if v.MultiContent && len(v.Updates) > 0 {
			latestUpdate := 0
			for update := range v.Updates {
				if update > latestUpdate {
					latestUpdate = update
				}
			}
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
			ops.skip(filepath.Join(v.File.ExtendedInfo.BaseFolder, v.File.ExtendedInfo.FileName), "split files are not organized")
			continue
		}

		destinationPath := v.File.ExtendedInfo.BaseFolder
		if !v.BaseExist {
			destinationPath = ""
		}

		//create folder if needed
		if options.CreateFolderPerGame {
			destinationPath = filepath.Join(baseFolder, getFolderName(options, templateData))
			if err := ops.mkdir(destinationPath); err != nil {
				continue
			}
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
			updateProgress.UpdateProgress(tasksSize, tasksSize, "deleting empty folders...")
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

func getTitleName(switchTitle *db.SwitchTitle, v *db.SwitchGameFiles) string {
	if switchTitle != nil && switchTitle.Attributes.Name != "" {
		res := cjk.FindAllString(switchTitle.Attributes.Name, -1)
		if len(res) == 0 {
			return switchTitle.Attributes.Name
		}
	}

	if v != nil && v.File.Metadata != nil && v.File.Metadata.Ncap != nil {
		name := v.File.Metadata.Ncap.TitleName["AmericanEnglish"].Title
		if name != "" {
			return name
		}
	}

	//for non eshop games (cartridge only), grab the name from the file
	if v != nil && v.File.ExtendedInfo.FileName != "" {
		return strings.TrimSpace(db.ParseTitleNameFromFileName(v.File.ExtendedInfo.FileName))
	}

	return "Unknown Title"
}

func getFolderName(options settings.OrganizeOptions, templateData map[string]string) string {
	return applyTemplate(templateData, options.SwitchSafeFileNames, options.FolderNameTemplate, 0)
}

func getFileName(options settings.OrganizeOptions, originalName string, templateData map[string]string, nameTry int) string {
	if !options.RenameFiles {
		return originalName
	}
	ext := path.Ext(originalName)
	result := applyTemplate(templateData, options.SwitchSafeFileNames, options.FileNameTemplate, nameTry)
	return result + ext
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
	return folderIllegalCharsRegex.ReplaceAllString(result, "")
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
