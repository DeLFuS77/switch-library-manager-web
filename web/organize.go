package web

import (
	"encoding/json"
	"errors"
	"github.com/dtrunk90/switch-library-manager-web/db"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/process"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

const (
	ORGANIZE_ACTION_ORGANIZE = "organize"
	ORGANIZE_ACTION_CLEANUP  = "cleanup"
)

type OrganizePageData struct {
	GlobalPageData
	Settings *settings.AppSettings
}

type OrganizeForm struct {
	CreateFolderPerGame        bool   `in:"form=create_folder_per_game"`
	FolderNameTemplate         string `in:"form=folder_name_template"`
	RenameFiles                bool   `in:"form=rename_files"`
	FileNameTemplate           string `in:"form=file_name_template"`
	SwitchSafeFileNames        bool   `in:"form=switch_safe_file_names"`
	DlcFolder                  string `in:"form=dlc_folder"`
	UpdatesFolder              string `in:"form=updates_folder"`
	ProcessWhenMissingBaseGame bool   `in:"form=process_when_missing_base_game"`
	DeleteEmptyFolders         bool   `in:"form=delete_empty_folders"`
	DeleteDuplicateFiles       bool   `in:"form=delete_duplicate_files"`
	KeepDuplicate              string `in:"form=keep_duplicate"`
	KeepUpdatesWithCheats      bool   `in:"form=keep_updates_with_cheats"`
}

// duplicatesChanging counts the duplicates whose other copy is kept with the choice keep,
// from the library as it is: with the first one found, the order of the scan decides.
func (web *Web) duplicatesChanging(keep string) int {
	_, localDB := web.state.get()
	if localDB == nil || keep == settings.KEEP_FIRST_FOUND {
		return 0
	}
	files := map[string]db.ExtendedFileInfo{}
	titles := map[string]int{}
	add := func(file db.ExtendedFileInfo) {
		path := filepath.Join(file.BaseFolder, file.FileName)
		files[path] = file
		titles[path]++
	}
	for _, local := range localDB.TitlesMap {
		if local.BaseExist {
			add(local.File.ExtendedInfo)
		}
		for _, update := range local.Updates {
			add(update.ExtendedInfo)
		}
		for _, dlc := range local.Dlc {
			add(dlc.ExtendedInfo)
		}
	}
	count := 0
	for file, skipped := range localDB.Skipped {
		if skipped.ReasonCode != db.REASON_DUPLICATE {
			continue
		}
		kept, ok := files[keptBy(skipped.ReasonText)]
		path := filepath.Join(file.BaseFolder, file.FileName)
		if ok && db.PreferDuplicate(keep, kept, titles[keptBy(skipped.ReasonText)], file, max(titles[path], 1)) {
			count++
		}
	}
	return count
}

func (f *OrganizeForm) toOptions(current settings.OrganizeOptions) settings.OrganizeOptions {
	current.CreateFolderPerGame = f.CreateFolderPerGame
	current.FolderNameTemplate = strings.TrimSpace(f.FolderNameTemplate)
	current.RenameFiles = f.RenameFiles
	current.FileNameTemplate = strings.TrimSpace(f.FileNameTemplate)
	current.SwitchSafeFileNames = f.SwitchSafeFileNames
	current.DlcFolder = strings.TrimSpace(f.DlcFolder)
	current.UpdatesFolder = strings.TrimSpace(f.UpdatesFolder)
	current.ProcessWhenMissingBaseGame = f.ProcessWhenMissingBaseGame
	current.DeleteEmptyFolders = f.DeleteEmptyFolders
	current.DeleteDuplicateFiles = f.DeleteDuplicateFiles
	current.DeleteUpdatesWithCheats = !f.KeepUpdatesWithCheats
	current.KeepDuplicate = settings.KEEP_FIRST_FOUND
	for _, choice := range settings.KeepDuplicateChoices {
		if f.KeepDuplicate == choice {
			current.KeepDuplicate = choice
		}
	}
	return current
}

type ApiOperation struct {
	Kind   string `json:"kind"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Reason string `json:"reason,omitempty"`
	Error  string `json:"error,omitempty"`
}

type OrganizeResponse struct {
	DryRun        bool           `json:"dryRun"`
	LibraryFolder string         `json:"libraryFolder"`
	Operations    []ApiOperation `json:"operations"`
	Changes       int            `json:"changes"`
	Errors        int            `json:"errors"`
	// files kept because the copy that stays was not checked yet
	NeedsVerify int `json:"needsVerify,omitempty"`
}

// displayPath shortens paths inside the library folder to keep the preview readable.
func displayPath(libraryFolder string, path string) string {
	if path == "" || libraryFolder == "" {
		return path
	}
	if rel, err := filepath.Rel(libraryFolder, path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return rel
	}
	return path
}

func newOrganizeResponse(operations []process.Operation, dryRun bool, libraryFolder string, lang string) OrganizeResponse {
	response := OrganizeResponse{DryRun: dryRun, Operations: []ApiOperation{}, LibraryFolder: libraryFolder}
	for _, op := range operations {
		apiOp := ApiOperation(op)
		apiOp.From = displayPath(libraryFolder, op.From)
		apiOp.To = displayPath(libraryFolder, op.To)
		apiOp.Error = translate(lang, op.Error)
		apiOp.Reason = translate(lang, op.Reason)
		response.Operations = append(response.Operations, apiOp)
		if op.Reason == NOTE_SPACE_VERIFY_KEPT || op.Reason == NOTE_SPACE_VERIFY {
			response.NeedsVerify++
		}
		if op.Error != "" {
			response.Errors++
		} else if op.Kind != process.OP_SKIP && op.Kind != process.OP_CLEANUP {
			response.Changes++
		}
	}
	return response
}

// organize plans (dryRun) or performs an organize action on the current library.
func (web *Web) organize(action string, dryRun bool) ([]process.Operation, error) {
	switchDB, localDB := web.state.get()
	if localDB == nil {
		return nil, errors.New("the library has not been scanned yet")
	}

	settingsObj := settings.ReadSettings(web.dataFolder)
	options := settingsObj.OrganizeOptions
	// a preview shows no progress: it would take over the one of a scan running meanwhile
	var progress db.ProgressUpdater = web
	if dryRun {
		progress = nil
	}

	switch action {
	case ORGANIZE_ACTION_ORGANIZE:
		return process.OrganizeByFolders(settingsObj.Folder, localDB, switchDB, options, dryRun, progress)
	case ORGANIZE_ACTION_CLEANUP:
		// of two copies the compressed one stays, when it was checked and is sound
		return process.DeleteOldUpdates(settingsObj.Folder, web.preferCompressed(localDB), options, dryRun, progress, web.cleanupRefusal), nil
	default:
		return nil, errors.New("unknown action")
	}
}

// runOrganizeAction organizes the files or deletes the old updates as a task. The caller holds
// the synchronization (startSync), so no scan moves files at the same time, and rescans after.
func (web *Web) runOrganizeAction(action string, trigger string) ([]process.Operation, error) {
	kind := TASK_ORGANIZE
	if action == ORGANIZE_ACTION_CLEANUP {
		kind = TASK_CLEANUP
	}
	// files are moved or deleted: no compression, conversion, pack or check may start
	// meanwhile, and none may be running
	if !web.organizing.CompareAndSwap(false, true) {
		return nil, errOrganizeBusy
	}
	defer web.organizing.Store(false)
	if web.compressionRunning() || web.measuring.Load() {
		return nil, errOrganizeBusy
	}
	taskId := web.startTask(kind, trigger)
	operations, err := web.organize(action, false)
	web.verifications().follow(operations)
	var failure *TaskNote
	if err == nil {
		web.taskLog().SetResult(taskId, 0, len(operations))
	} else {
		failure = &TaskNote{Text: NOTE_ORGANIZE_FAILED, Detail: err.Error()}
	}
	web.finishTask(taskId, failure)
	return operations, err
}

// removeFilesAsTask deletes files of the library as a cleanup task, while no scan,
// compression, conversion, pack, check or copy can start, and gives what was deleted.
func (web *Web) removeFilesAsTask(files []db.ExtendedFileInfo, reasons []string) (int, int64, error) {
	if !web.organizing.CompareAndSwap(false, true) {
		return 0, 0, errOrganizeBusy
	}
	defer web.organizing.Store(false)
	if web.compressionRunning() || web.measuring.Load() {
		return 0, 0, errOrganizeBusy
	}
	if !web.state.startSync() {
		return 0, 0, errRegionBusy
	}
	defer web.state.endSync()
	taskId := web.startTask(TASK_CLEANUP, TRIGGER_MANUAL)
	deleted, freed := 0, int64(0)
	var failure *TaskNote
	removed := process.RemoveFiles(files, reasons, false)
	web.verifications().follow(removed)
	for i, op := range removed {
		if op.Error == "" {
			deleted++
			freed += files[i].Size
		} else if failure == nil {
			failure = &TaskNote{Text: NOTE_ORGANIZE_FAILED, Detail: op.From + ": " + op.Error}
		}
	}
	web.taskLog().SetCompressResult(taskId, deleted, freed)
	web.finishTask(taskId, failure)
	return deleted, freed, nil
}

// errOrganizeBusy is returned while another task reads or writes the files of the library.
var errOrganizeBusy = errorString("Another task is working on the files of the library (organizing, a cleanup, a compression, a pack or a check): try again when it has finished.")

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeGlobalError(w http.ResponseWriter, status int, lang string, message string) {
	writeJSON(w, status, ErrorResponse{GlobalError: GlobalError{StrongMessage: translate(lang, "Error!"), Message: translate(lang, message)}, FieldErrors: []FieldError{}})
}

func (web *Web) HandleOrganize() {
	fsPatterns := []string{
		"resources/layout.html",
		"resources/pages/organize.html",
	}

	web.HandleValidated("/organize.html", OrganizeForm{}, func() any {
		return OrganizePageData{
			GlobalPageData: web.globalPageData("organize"),
			Settings:       settings.ReadSettings(web.dataFolder),
		}
	}, func(value any, lang string) ErrorResponse {
		form := value.(*OrganizeForm)
		errorResponse := ErrorResponse{FieldErrors: []FieldError{}}

		options := form.toOptions(settings.ReadSettings(web.dataFolder).OrganizeOptions)
		if err := process.ValidateOptions(options); err != nil {
			field := "file_name_template"
			if strings.Contains(err.Error(), "folder") {
				field = "folder_name_template"
			}
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: field, Message: translate(lang, err.Error())})
		}

		for field, folder := range map[string]string{"dlc_folder": options.DlcFolder, "updates_folder": options.UpdatesFolder} {
			if strings.Contains(folder, "..") {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: field, Message: translate(lang, "Parent folder references (..) are not allowed")})
			}
		}

		return errorResponse
	}, func(value any, lang string) SuccessResponse {
		form := value.(*OrganizeForm)
		keptBefore := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepDuplicate
		settings.UpdateSettings(web.dataFolder, func(appSettings *settings.AppSettings) {
			appSettings.OrganizeOptions = form.toOptions(appSettings.OrganizeOptions)
		})
		// the tune-up counts the changes of Organize with the new options
		web.invalidateDerived()
		// the scan decides which copy of a duplicate is kept
		if keep := settings.ReadSettings(web.dataFolder).OrganizeOptions.KeepDuplicate; keep != keptBefore {
			changing := web.duplicatesChanging(keep)
			web.Rescan(TRIGGER_SETTINGS)
			return SuccessResponse{
				StrongMessage: translate(lang, "Saved!"),
				Message:       translatef(lang, "The library is scanned again: %v duplicates keep the other copy now.", changing),
			}
		}

		return SuccessResponse{
			StrongMessage: translate(lang, "Saved!"),
			Message:       translate(lang, "Use Preview to see what will change."),
		}
	}, web.embedFS, fsPatterns...)

	web.handleOrganizeActions()
}

func (web *Web) handleOrganizeActions() {
	web.router.HandleFunc("/organize/preview", func(w http.ResponseWriter, r *http.Request) {
		operations, err := web.organize(r.FormValue("action"), true)
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), err.Error())
			return
		}
		writeJSON(w, http.StatusOK, newOrganizeResponse(operations, true, settings.ReadSettings(web.dataFolder).Folder, web.requestLanguage(r)))
	}).Methods("POST")

	web.router.HandleFunc("/organize/run", func(w http.ResponseWriter, r *http.Request) {
		// never move files while the library is being scanned
		if !web.state.startSync() {
			writeGlobalError(w, http.StatusConflict, web.requestLanguage(r), "A synchronization is running, try again when it has finished.")
			return
		}

		operations, err := web.runOrganizeAction(r.FormValue("action"), TRIGGER_MANUAL)
		web.state.endSync()

		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, web.requestLanguage(r), err.Error())
			return
		}

		// the cached library no longer matches the files on disk
		web.Rescan(TRIGGER_ORGANIZE)

		writeJSON(w, http.StatusOK, newOrganizeResponse(operations, false, settings.ReadSettings(web.dataFolder).Folder, web.requestLanguage(r)))
	}).Methods("POST")
}
