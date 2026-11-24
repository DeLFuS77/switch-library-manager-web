package web

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/activity"
	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// reportZip is a ZIP of small files with these names.
func reportZip(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for _, name := range names {
		writer, err := archive.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		writer.Write([]byte("log of " + name))
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// sendReport sends a report with c, the body read as it is.
func sendReport(c *client, body io.Reader, contentType string, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	request := httptest.NewRequest("POST", "/install/report", body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.RemoteAddr = "192.168.1.10:1234"
	if c.ip != "" {
		request.RemoteAddr = c.ip + ":1234"
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	for _, cookie := range c.cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, request)
	return recorder
}

// A report is kept under the time it came, noted in the activity, and only the newest 10 of a
// console stay.
func TestNetReportStoredAndKept(t *testing.T) {
	web, _, _, console, auth, keyId, _ := pushWeb(t)
	data := reportZip(t, "nxdock.log", "logs/install.log", "logs/")
	got := sendReport(console, bytes.NewReader(data), "application/zip", "Authorization", auth)
	var answer struct {
		Stored string `json:"stored"`
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &answer) != nil || !netReportNamePattern.MatchString(answer.Stored) {
		t.Fatalf("stored: %d %s", got.Code, got.Body.String())
	}
	kept, err := os.ReadFile(filepath.Join(web.dataFolder, "consoles", "reports", keyId, answer.Stored))
	if err != nil || !bytes.Equal(kept, data) {
		t.Fatalf("the report as it came: %v", err)
	}
	noted := false
	for _, entry := range web.activities().List() {
		if entry.Action == activity.ACTION_NET_REPORT && entry.User == "bob" && entry.Detail == `"Switch": `+answer.Stored {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("noted: %+v", web.activities().List())
	}

	names := []string{answer.Stored}
	for i := 0; i < netReportsKeep+1; i++ {
		got := sendReport(console, bytes.NewReader(reportZip(t, fmt.Sprintf("report %d.txt", i))), "application/zip", "Authorization", auth)
		json.Unmarshal(got.Body.Bytes(), &answer)
		if got.Code != http.StatusOK || answer.Stored == names[len(names)-1] {
			t.Fatalf("report %d: %d %s", i, got.Code, got.Body.String())
		}
		names = append(names, answer.Stored)
	}
	left := web.netReportNames(keyId)
	if len(left) != netReportsKeep || left[0] != names[2] || left[len(left)-1] != names[len(names)-1] {
		t.Fatalf("the newest 10: %v of %v", left, names)
	}
}

// What is not a ZIP of plain files with relative names inside it, has too many files or is too
// big is refused, and so is a report without the key of a console, or with the network install
// off.
func TestNetReportRefused(t *testing.T) {
	web, admin, bob, console, auth, keyId, _ := pushWeb(t)
	var link bytes.Buffer
	archive := zip.NewWriter(&link)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	writer, _ := archive.CreateHeader(header)
	writer.Write([]byte("/etc/passwd"))
	archive.Close()
	many := []string{}
	for i := 0; i <= netReportMaxEntries; i++ {
		many = append(many, fmt.Sprintf("file %d.txt", i))
	}
	for name, body := range map[string][]byte{
		"climbs out":      reportZip(t, "logs/../../settings.json"),
		"climbs out too":  reportZip(t, "../x.txt"),
		"from the root":   reportZip(t, "/etc/x.txt"),
		"a drive":         reportZip(t, "C:/x.txt"),
		"a back slash":    reportZip(t, `logs\..\x.txt`),
		"a symbolic link": link.Bytes(),
		"empty":           reportZip(t),
		"too many files":  reportZip(t, many...),
		"not a ZIP":       []byte("plain text"),
	} {
		if code := sendReport(console, bytes.NewReader(body), "application/zip", "Authorization", auth).Code; code != http.StatusBadRequest {
			t.Errorf("%s: %d", name, code)
		}
	}
	big := make([]byte, netReportMaxSize+1)
	if code := sendReport(console, bytes.NewReader(big), "application/zip", "Authorization", auth).Code; code != http.StatusRequestEntityTooLarge {
		t.Errorf("too big: %d", code)
	}
	// without a length told
	if code := sendReport(console, io.MultiReader(bytes.NewReader(big)), "application/zip", "Authorization", auth).Code; code != http.StatusRequestEntityTooLarge {
		t.Errorf("too big, without a length: %d", code)
	}
	good := reportZip(t, "nxdock.log")
	if code := sendReport(console, bytes.NewReader(good), "application/json", "Authorization", auth).Code; code != http.StatusUnsupportedMediaType {
		t.Errorf("not sent as a ZIP: %d", code)
	}
	if code := sendReport(&client{t: t, handler: admin.handler}, bytes.NewReader(good), "application/zip").Code; code != http.StatusUnauthorized {
		t.Errorf("without a key: %d", code)
	}
	if code := sendReport(bob, bytes.NewReader(good), "application/zip").Code; code != http.StatusForbidden {
		t.Errorf("a user is not a console: %d", code)
	}
	settings.UpdateSettings(web.dataFolder, func(s *settings.AppSettings) { s.NetInstall.Enabled = false })
	if code := sendReport(console, bytes.NewReader(good), "application/zip", "Authorization", auth).Code; code != http.StatusNotFound {
		t.Errorf("turned off: %d", code)
	}
	if names := web.netReportNames(keyId); len(names) != 0 {
		t.Fatalf("nothing kept: %v", names)
	}
}

// Only an administrator lists, downloads and deletes the reports, never in the demo.
func TestNetReportsForAdministrators(t *testing.T) {
	web, admin, bob, console, auth, keyId, _ := pushWeb(t)
	data := reportZip(t, "nxdock.log")
	var answer struct {
		Stored string `json:"stored"`
	}
	json.Unmarshal(sendReport(console, bytes.NewReader(data), "application/zip", "Authorization", auth).Body.Bytes(), &answer)
	path := "/api/install/reports/" + keyId + "/" + answer.Stored

	var list struct {
		Reports []netReportRow `json:"reports"`
	}
	got := admin.do("GET", "/api/install/reports", nil)
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &list) != nil || len(list.Reports) != 1 {
		t.Fatalf("the list: %d %s", got.Code, got.Body.String())
	}
	if report := list.Reports[0]; report.KeyId != keyId || report.Console != "Switch" || report.Owner != "bob" || report.Name != answer.Stored ||
		report.Size != int64(len(data)) || report.Date.IsZero() {
		t.Fatalf("the report: %+v", report)
	}

	if code := bob.do("GET", "/api/install/reports", nil).Code; code != http.StatusForbidden {
		t.Fatalf("a player lists: %d", code)
	}
	if code := bob.do("GET", path, nil).Code; code != http.StatusForbidden {
		t.Fatalf("a player downloads: %d", code)
	}
	if code := bob.do("DELETE", path, nil).Code; code != http.StatusForbidden {
		t.Fatalf("a player deletes: %d", code)
	}
	if code := console.do("GET", "/api/install/reports", nil, "Authorization", auth).Code; code == http.StatusOK {
		t.Fatalf("a console lists: %d", code)
	}

	download := admin.do("GET", path, nil)
	if download.Code != http.StatusOK || !bytes.Equal(download.Body.Bytes(), data) ||
		!strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment;") || download.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("the download: %d %v", download.Code, download.Header())
	}
	for _, wrong := range []string{"/api/install/reports/" + keyId + "/..%2f..%2fsettings.json", "/api/install/reports/" + keyId + "/console.json",
		"/api/install/reports/../" + answer.Stored, "/api/install/reports/0000000000000000/" + answer.Stored} {
		if code := admin.do("GET", wrong, nil).Code; code == http.StatusOK {
			t.Fatalf("%s: %d", wrong, code)
		}
	}

	t.Setenv("SLM_DEMO", "true")
	if code := admin.do("GET", "/api/install/reports", nil).Code; code != http.StatusNotFound {
		t.Fatalf("in the demo: %d", code)
	}
	os.Unsetenv("SLM_DEMO")

	if got := admin.do("DELETE", path, nil); got.Code != http.StatusOK {
		t.Fatalf("deleted: %d %s", got.Code, got.Body.String())
	}
	if _, err := os.Stat(filepath.Join(web.netReportsRoot(), keyId)); !os.IsNotExist(err) {
		t.Fatalf("the folder goes with the last report: %v", err)
	}
	if code := admin.do("DELETE", path, nil).Code; code != http.StatusNotFound {
		t.Fatalf("deleted twice: %d", code)
	}
	list.Reports = nil
	json.Unmarshal(admin.do("GET", "/api/install/reports", nil).Body.Bytes(), &list)
	if len(list.Reports) != 0 {
		t.Fatalf("none left: %+v", list.Reports)
	}
}

// Diagnostics lists the reports of the consoles for the administrators, with Download and Delete,
// and says when there is none; not in the demo.
func TestNetReportsInDiagnostics(t *testing.T) {
	web, admin, _, console, auth, keyId, _ := pushWeb(t)
	web.embedFS = os.DirFS("..")
	web.HandleDiagnostics()
	page := admin.do("GET", "/diagnostics.html", nil).Body.String()
	if !strings.Contains(page, "Reports from the consoles") || !strings.Contains(page, "No console has sent a report yet") || strings.Contains(page, "data-console-report-delete") {
		t.Fatalf("none yet: %s", page)
	}
	var answer struct {
		Stored string `json:"stored"`
	}
	json.Unmarshal(sendReport(console, bytes.NewReader(reportZip(t, "nxdock.log")), "application/zip", "Authorization", auth).Body.Bytes(), &answer)
	path := "/api/install/reports/" + keyId + "/" + answer.Stored
	page = admin.do("GET", "/diagnostics.html", nil).Body.String()
	for _, want := range []string{`href="` + path + `" download`, `data-console-report-delete="` + path + `"`, ">Switch</td>", "<span>bob</span>", "The last 10 of each console are kept",
		"Delete the report of Switch from"} {
		if !strings.Contains(page, want) {
			t.Errorf("Diagnostics has no %s", want)
		}
	}
	t.Setenv("SLM_DEMO", "true")
	if page := admin.do("GET", "/diagnostics.html", nil).Body.String(); strings.Contains(page, "Reports from the consoles") {
		t.Error("not in the demo")
	}
}

// The card of a console on the page Consoles says how many reports it sent, with the newest to
// download and the way to Diagnostics, for an administrator only; not in the demo (#205).
func TestNetReportsOnTheConsoleCard(t *testing.T) {
	web, admin, bob, console, auth, _ := consolesWeb(t)
	created := createKey(t, admin, "Switch salon", ROLE_CONSOLE)
	adminAuth := bearer(created.Key)
	page := admin.do("GET", "/consoles.html", nil).Body.String()
	if !strings.Contains(page, "Switch salon") || strings.Contains(page, "console-reports-line") {
		t.Fatalf("no report yet: %s", page)
	}
	var first, second struct {
		Stored string `json:"stored"`
	}
	json.Unmarshal(sendReport(console, bytes.NewReader(reportZip(t, "nxdock.log")), "application/zip", "Authorization", adminAuth).Body.Bytes(), &first)
	data := reportZip(t, "nxdock.log", "history.tsv")
	json.Unmarshal(sendReport(console, bytes.NewReader(data), "application/zip", "Authorization", adminAuth).Body.Bytes(), &second)
	if first.Stored == "" || second.Stored == "" {
		t.Fatal("the reports were not kept")
	}
	page = admin.do("GET", "/consoles.html", nil).Body.String()
	for _, want := range []string{"console-reports-line", "2 reports from NXDock", `href="/api/install/reports/` + created.ID + "/" + second.Stored + `" download`,
		"Download the newest", `href="/diagnostics.html#consoleReportsTitle"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the card has no %s", want)
		}
	}
	if strings.Contains(page, first.Stored) {
		t.Error("only the newest is offered")
	}

	// bob's console sent one too, but bob is not an administrator
	sendReport(console, bytes.NewReader(reportZip(t, "nxdock.log")), "application/zip", "Authorization", auth)
	if page := bob.do("GET", "/consoles.html", nil).Body.String(); strings.Contains(page, "console-reports-line") || strings.Contains(page, "from NXDock") {
		t.Error("a player sees no report")
	}

	if n, last := web.netConsoleReports(created.ID); n != 2 || last == nil || last.Name != second.Stored || last.Size != int64(len(data)) {
		t.Errorf("the reports of the console: %d %+v", n, last)
	}
	if n, last := web.netConsoleReports("0123456789abcdef"); n != 0 || last != nil {
		t.Errorf("a console without reports: %d %+v", n, last)
	}

	t.Setenv("SLM_DEMO", "true")
	if page := admin.do("GET", "/consoles.html", nil).Body.String(); strings.Contains(page, "console-reports-line") {
		t.Error("not in the demo")
	}
}
