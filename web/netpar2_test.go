package web

import (
	"bytes"
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/par2"
)

func serveChecked(t *testing.T, path string, rangeHeader string) (*httptest.ResponseRecorder, []string) {
	t.Helper()
	source, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	stat, _ := source.Stat()
	told := []string{}
	reader := newCheckedReader(source, path, stat.Size(), func() error { return repairNetFile(context.Background(), path) }, func(s string) { told = append(told, s) })
	if reader == nil {
		t.Fatal("no checked reader for a file with recovery data")
	}
	request := httptest.NewRequest(http.MethodGet, "/file", nil)
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response := httptest.NewRecorder()
	http.ServeContent(response, request, "", time.Time{}, reader)
	return response, told
}

// A game damaged on the disk is rebuilt with its recovery data before it is sent: the console
// gets it whole, from the start or from a range, and the file on the disk is whole again.
func TestNetSendRepairsWithRecoveryData(t *testing.T) {
	folder := t.TempDir()
	path := filepath.Join(folder, "Game.nsz")
	original := make([]byte, 700000)
	rand.New(rand.NewSource(7)).Read(original)
	os.WriteFile(path, original, 0o644)
	if err := par2.Create(context.Background(), path, par2.Path(path), 5, nil); err != nil {
		t.Fatal(err)
	}

	damaged := append([]byte(nil), original...)
	damaged[300000] ^= 1
	os.WriteFile(path, damaged, 0o644)
	response, told := serveChecked(t, path, "")
	if !bytes.Equal(response.Body.Bytes(), original) || len(told) != 1 {
		t.Fatalf("sent %d bytes, whole %v; told %v", response.Body.Len(), bytes.Equal(response.Body.Bytes(), original), told)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Fatal("the file is not whole again on the disk")
	}

	os.WriteFile(path, damaged, 0o644)
	response, _ = serveChecked(t, path, "bytes=299990-300010")
	if response.Code != http.StatusPartialContent || !bytes.Equal(response.Body.Bytes(), original[299990:300011]) {
		t.Fatalf("range: %d %v", response.Code, response.Body.Bytes())
	}

	// too damaged: what is sent stops before the damaged slice
	for i := 0; i < len(damaged); i += 4096 {
		damaged[i] ^= 1
	}
	os.WriteFile(path, damaged, 0o644)
	response, told = serveChecked(t, path, "")
	if response.Body.Len() == len(original) || len(told) == 0 {
		t.Fatalf("a file too damaged was sent whole: %d bytes, %v", response.Body.Len(), told)
	}

	// a file without recovery data, or whose recovery data does not fit it, is sent as it is
	os.Remove(par2.Path(path))
	source, _ := os.Open(path)
	defer source.Close()
	if newCheckedReader(source, path, int64(len(damaged)), nil, nil) != nil {
		t.Fatal("a reader for a file without recovery data")
	}
}
