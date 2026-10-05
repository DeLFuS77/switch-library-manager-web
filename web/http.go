package web

import (
	"encoding/json"
	"fmt"
	"github.com/ggicci/httpin"
	"github.com/justinas/alice"
	"io/fs"
	"log"
	"net/http"
)

type ErrorResponse struct {
	GlobalError GlobalError  `json:"globalError"`
	FieldErrors []FieldError `json:"fieldErrors"`
}

type GlobalError struct {
	StrongMessage string `json:"strongMessage"`
	Message       string `json:"message"`
}

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type SuccessResponse struct {
	StrongMessage string `json:"strongMessage"`
	Message       string `json:"message"`
}

type FilteredPageData func(filter *TitleItemFilter) any
type Validate func(value any, lang string) ErrorResponse
type OnSuccess func(value any, lang string) SuccessResponse
type PageData func() any

func hasErrors(errorResponse ErrorResponse) bool {
	return errorResponse.GlobalError != GlobalError{} || len(errorResponse.FieldErrors) > 0
}

func (web *Web) mustParseTemplates(fs fs.FS, fsPatterns ...string) templateSet {
	templates, err := parseTemplates(fs, fsPatterns...)
	if err != nil {
		web.sugarLogger.Error(fmt.Errorf("parsing template failed: %w", err))
		log.Fatal(err)
	}
	return templates
}

func (web *Web) render(w http.ResponseWriter, r *http.Request, templates templateSet, data any) {
	if err := templates.execute(w, web.requestLanguage(r), data); err != nil {
		web.sugarLogger.Error(fmt.Errorf("executing template failed: %w", err))
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (web *Web) HandleFiltered(pattern string, filteredPageData FilteredPageData, fs fs.FS, fsPatterns ...string) {
	templates := web.mustParseTemplates(fs, fsPatterns...)

	web.router.Handle(pattern, alice.New(httpin.NewInput(TitleItemFilter{})).ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		filter := r.Context().Value(httpin.Input).(*TitleItemFilter)
		filter.Normalize()
		web.render(w, r, templates, filteredPageData(filter))
	}))
}

func (web *Web) HandleValidated(pattern string, inputStruct interface{}, pageData PageData, validate Validate, onSuccess OnSuccess, fs fs.FS, fsPatterns ...string) {
	templates := web.mustParseTemplates(fs, fsPatterns...)

	web.router.Handle(pattern, alice.New(httpin.NewInput(inputStruct)).ThenFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
			case "GET":
				web.render(w, r, templates, pageData())
			case "POST":
				w.Header().Set("Content-Type", "application/json")

				value := r.Context().Value(httpin.Input)
				jsonEncoder := json.NewEncoder(w)
				lang := web.requestLanguage(r)

				if errorResponse := validate(value, lang); hasErrors(errorResponse) {
					w.WriteHeader(http.StatusBadRequest)
					jsonEncoder.Encode(errorResponse)
					return
				}

				jsonEncoder.Encode(onSuccess(value, lang))
			default:
				web.sugarLogger.Error(fmt.Errorf("Unsupported method: %s", r.Method))
				w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
}

func (web *Web) Handle(pattern string, pageData PageData, fs fs.FS, fsPatterns ...string) {
	templates := web.mustParseTemplates(fs, fsPatterns...)

	web.router.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		web.render(w, r, templates, pageData())
	})
}
