package web

import (
	"os"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	"go.uber.org/zap"
)

// sharedDemo is one demo library for the tests that only read it: making the demo draws
// every cover, which is slow with the race detector.
var sharedDemo struct {
	once sync.Once
	web  *Web
	dir  string
}

func demoWeb(t *testing.T) *Web {
	t.Helper()
	sharedDemo.once.Do(func() {
		dir, err := os.MkdirTemp("", "slm-demo-test")
		if err != nil {
			t.Fatal(err)
		}
		web := &Web{router: mux.NewRouter(), dataFolder: dir, sugarLogger: zap.NewNop().Sugar(), embedFS: os.DirFS("..")}
		web.loadDemo()
		web.HandleQuickSearch()
		web.HandleYear()
		sharedDemo.web, sharedDemo.dir = web, dir
	})
	if sharedDemo.web == nil {
		t.Fatal("the demo could not be made")
	}
	return sharedDemo.web
}

// removeSharedDemo removes the folder of the shared demo after the tests.
func removeSharedDemo() {
	if sharedDemo.web != nil && sharedDemo.web.store != nil {
		sharedDemo.web.store.Close()
	}
	if sharedDemo.dir != "" {
		os.RemoveAll(sharedDemo.dir)
	}
}
