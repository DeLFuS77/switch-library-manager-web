package edensync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// HTTPLibrary is Switch Library Manager Web over HTTP, with the key a computer got when it was
// paired (AskPairing, CheckPairing). It is a Library.
type HTTPLibrary struct {
	// the address of the app, such as http://192.168.1.2:3000
	BaseURL string
	Key     string
	// http.DefaultClient when nil
	Client *http.Client
}

func (l *HTTPLibrary) client() *http.Client {
	if l.Client != nil {
		return l.Client
	}
	return http.DefaultClient
}

// StatusError is an answer of the library that is not a success.
type StatusError struct {
	Status  int
	Message string
}

func (e *StatusError) Error() string {
	if e.Message == "" {
		return "the library answered " + strconv.Itoa(e.Status)
	}
	return fmt.Sprintf("the library answered %d: %s", e.Status, e.Message)
}

// statusError reads the error of an answer: the message of its JSON, or its first line.
func statusError(response *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	var answer struct {
		Error       string `json:"error"`
		GlobalError struct {
			Message string `json:"message"`
		} `json:"globalError"`
	}
	message := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])
	if json.Unmarshal(data, &answer) == nil {
		if answer.Error != "" {
			message = answer.Error
		} else if answer.GlobalError.Message != "" {
			message = answer.GlobalError.Message
		}
	}
	return &StatusError{Status: response.StatusCode, Message: message}
}

func (l *HTTPLibrary) request(ctx context.Context, method, path string, query url.Values, body io.Reader, size int64, kind string) (*http.Response, error) {
	address := strings.TrimRight(l.BaseURL, "/") + path
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		request.ContentLength = size
		request.Header.Set("Content-Type", kind)
	}
	request.Header.Set("Authorization", "Bearer "+l.Key)
	request.Header.Set("Accept", "application/json")
	response, err := l.client().Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		defer response.Body.Close()
		return nil, statusError(response)
	}
	return response, nil
}

// call sends a request with a JSON body (when in is not nil) and reads the JSON answer into out.
func (l *HTTPLibrary) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	var size int64
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body, size = bytes.NewReader(data), int64(len(data))
	}
	response, err := l.request(ctx, method, path, nil, body, size, "application/json")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if out == nil {
		io.Copy(io.Discard, response.Body)
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// Saves is GET /install/saves.
func (l *HTTPLibrary) Saves(ctx context.Context) ([]LibrarySave, error) {
	var answer struct {
		Saves []LibrarySave `json:"saves"`
	}
	if err := l.call(ctx, http.MethodGet, "/install/saves", nil, &answer); err != nil {
		return nil, err
	}
	return answer.Saves, nil
}

// Download is GET /install/saves/{titleId}/{id}.
func (l *HTTPLibrary) Download(ctx context.Context, titleId, id string, out io.Writer) error {
	response, err := l.request(ctx, http.MethodGet, "/install/saves/"+url.PathEscape(titleId)+"/"+url.PathEscape(id), nil, nil, 0, "")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, err = io.Copy(out, response.Body)
	return err
}

// Upload is POST /install/saves/{titleId}?emulator=…&account=…&profile=…&auto=1 with the ZIP.
func (l *HTTPLibrary) Upload(ctx context.Context, upload Upload) (LibrarySave, error) {
	query := url.Values{"emulator": {string(upload.Emulator)}, "account": {upload.Profile.Name}, "profile": {upload.Profile.ID.String()}}
	if upload.Auto {
		query.Set("auto", "1")
	}
	response, err := l.request(ctx, http.MethodPost, "/install/saves/"+url.PathEscape(upload.TitleId), query, upload.Body, upload.Size, "application/zip")
	if err != nil {
		return LibrarySave{}, err
	}
	defer response.Body.Close()
	var answer struct {
		Id   string `json:"id"`
		When int64  `json:"when"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(response.Body).Decode(&answer); err != nil {
		return LibrarySave{}, err
	}
	return LibrarySave{TitleId: upload.TitleId, Id: answer.Id, When: answer.When, Size: answer.Size, Emulator: true, Console: upload.Emulator.Name(),
		Account: upload.Profile.Name, Profile: upload.Profile.ID.String(), Auto: upload.Auto}, nil
}

// Pairs is GET /install/saves/profiles.
func (l *HTTPLibrary) Pairs(ctx context.Context) (Pairs, error) {
	var pairs Pairs
	err := l.call(ctx, http.MethodGet, "/install/saves/profiles", nil, &pairs)
	return pairs, err
}

// ReportProfiles is PUT /install/saves/profiles.
func (l *HTTPLibrary) ReportProfiles(ctx context.Context, emulator Emulator, profiles []ProfileReport) (Pairs, error) {
	var pairs Pairs
	err := l.call(ctx, http.MethodPut, "/install/saves/profiles", map[string]any{"emulator": emulator, "profiles": profiles}, &pairs)
	return pairs, err
}

// ReportConflict is POST /install/saves/conflicts.
func (l *HTTPLibrary) ReportConflict(ctx context.Context, conflict Conflict) (string, error) {
	var answer struct {
		Id string `json:"id"`
	}
	err := l.call(ctx, http.MethodPost, "/install/saves/conflicts", conflict, &answer)
	return answer.Id, err
}

// Conflicts is GET /install/saves/conflicts.
func (l *HTTPLibrary) Conflicts(ctx context.Context) ([]Conflict, error) {
	var answer struct {
		Conflicts []Conflict `json:"conflicts"`
	}
	err := l.call(ctx, http.MethodGet, "/install/saves/conflicts", nil, &answer)
	return answer.Conflicts, err
}

// ConflictDone is POST /install/saves/conflicts/{id}/done.
func (l *HTTPLibrary) ConflictDone(ctx context.Context, id string) error {
	return l.call(ctx, http.MethodPost, "/install/saves/conflicts/"+url.PathEscape(id)+"/done", nil, nil)
}

// PairRequest is a computer waiting to be paired: the code it shows, which the user checks in the
// library before accepting it, and the seconds it waits at most.
type PairRequest struct {
	Id      string `json:"id"`
	Code    string `json:"code"`
	Expires int    `json:"expires"`
}

// the states of a pairing
const (
	PairWaiting  = "waiting"
	PairAccepted = "accepted"
	PairRejected = "rejected"
	PairExpired  = "expired"
)

// PairAnswer is how a pairing goes; accepted, it holds the key, given once, and the name of the
// key and of the user.
type PairAnswer struct {
	State string `json:"state"`
	Key   string `json:"key,omitempty"`
	Name  string `json:"name,omitempty"`
	User  string `json:"user,omitempty"`
}

// AskPairing asks the library to pair this computer, named so (NXPress on DESKTOP-1), as NXDock
// asks for a console: POST /install/pair with the kind "pc".
func AskPairing(ctx context.Context, client *http.Client, baseURL, name string) (PairRequest, error) {
	library := &HTTPLibrary{BaseURL: baseURL, Client: client}
	data, _ := json.Marshal(map[string]string{"console": name, "kind": "pc"})
	address := strings.TrimRight(baseURL, "/") + "/install/pair"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(data))
	if err != nil {
		return PairRequest{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := library.client().Do(request)
	if err != nil {
		return PairRequest{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return PairRequest{}, statusError(response)
	}
	var pairing PairRequest
	err = json.NewDecoder(response.Body).Decode(&pairing)
	return pairing, err
}

// CheckPairing asks how a pairing goes: GET /install/pair/{id}, to ask every few seconds until it
// is not PairWaiting.
func CheckPairing(ctx context.Context, client *http.Client, baseURL, id string) (PairAnswer, error) {
	library := &HTTPLibrary{BaseURL: baseURL, Client: client}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/install/pair/"+url.PathEscape(id), nil)
	if err != nil {
		return PairAnswer{}, err
	}
	response, err := library.client().Do(request)
	if err != nil {
		return PairAnswer{}, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return PairAnswer{}, err
	}
	var answer PairAnswer
	known := json.Unmarshal(data, &answer) == nil && answer.State != ""
	switch {
	case known && (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusAccepted):
		return answer, nil
	case known && response.StatusCode == http.StatusForbidden:
		return PairAnswer{State: PairRejected}, nil
	case response.StatusCode == http.StatusGone:
		return PairAnswer{State: PairExpired}, nil
	}
	return PairAnswer{}, &StatusError{Status: response.StatusCode, Message: strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0])}
}
