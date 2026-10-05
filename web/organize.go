package web

import (
	"encoding/json"
	"errors"
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
	Operations []ApiOperation `json:"operations"`
	Changes    int            `json:"changes"`
	Errors     int            `json:"errors"`
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

func newOrganizeResponse(operations []process.Operation, dryRun bool, libraryFolder string) OrganizeResponse {
	response := OrganizeResponse{DryRun: dryRun, Operations: []ApiOperation{}, LibraryFolder: libraryFolder}
	for _, op := range operations {
		apiOp := ApiOperation(op)
		apiOp.From = displayPath(libraryFolder, op.From)
		apiOp.To = displayPath(libraryFolder, op.To)
		response.Operations = append(response.Operations, apiOp)
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

	switch action {
	case ORGANIZE_ACTION_ORGANIZE:
		return process.OrganizeByFolders(settingsObj.Folder, localDB, switchDB, options, dryRun, web)
	case ORGANIZE_ACTION_CLEANUP:
		return process.DeleteOldUpdates(settingsObj.Folder, localDB, options, dryRun, web), nil
	default:
		return nil, errors.New("unknown action")
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func writeGlobalError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ErrorResponse{GlobalError: GlobalError{StrongMessage: "Error!", Message: message}, FieldErrors: []FieldError{}})
}

func (web *Web) HandleOrganize() {
	fsPatterns := []string {
		"resources/layout.html",
		"resources/pages/organize.html",
	}

	web.HandleValidated("/organize.html", OrganizeForm{}, func() any {
		return OrganizePageData {
			GlobalPageData: web.globalPageData("organize"),
			Settings: settings.ReadSettings(web.dataFolder),
		}
	}, func(value any) ErrorResponse {
		form := value.(*OrganizeForm)
		errorResponse := ErrorResponse{FieldErrors: []FieldError{}}

		options := form.toOptions(settings.ReadSettings(web.dataFolder).OrganizeOptions)
		if err := process.ValidateOptions(options); err != nil {
			field := "file_name_template"
			if strings.Contains(err.Error(), "folder") {
				field = "folder_name_template"
			}
			errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: field, Message: err.Error()})
		}

		for field, folder := range map[string]string{"dlc_folder": options.DlcFolder, "updates_folder": options.UpdatesFolder} {
			if strings.Contains(folder, "..") {
				errorResponse.FieldErrors = append(errorResponse.FieldErrors, FieldError{Field: field, Message: "Parent folder references (..) are not allowed"})
			}
		}

		return errorResponse
	}, func(value any) SuccessResponse {
		form := value.(*OrganizeForm)
		appSettings := settings.ReadSettings(web.dataFolder)
		appSettings.OrganizeOptions = form.toOptions(appSettings.OrganizeOptions)
		settings.SaveSettings(appSettings, web.dataFolder)

		return SuccessResponse {
			StrongMessage: "Saved!",
			Message: "Use Preview to see what will change.",
		}
	}, web.embedFS, fsPatterns...)

	web.handleOrganizeActions()
}

func (web *Web) handleOrganizeActions() {
	web.router.HandleFunc("/organize/preview", func(w http.ResponseWriter, r *http.Request) {
		operations, err := web.organize(r.FormValue("action"), true)
		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, newOrganizeResponse(operations, true, settings.ReadSettings(web.dataFolder).Folder))
	}).Methods("POST")

	web.router.HandleFunc("/organize/run", func(w http.ResponseWriter, r *http.Request) {
		// never move files while the library is being scanned
		if !web.state.startSync() {
			writeGlobalError(w, http.StatusConflict, "A synchronization is running, try again when it has finished.")
			return
		}

		operations, err := web.organize(r.FormValue("action"), false)
		web.state.endSync()

		if err != nil {
			writeGlobalError(w, http.StatusBadRequest, err.Error())
			return
		}

		// the cached library no longer matches the files on disk
		web.Rescan()

		writeJSON(w, http.StatusOK, newOrganizeResponse(operations, false, settings.ReadSettings(web.dataFolder).Folder))
	}).Methods("POST")
}
