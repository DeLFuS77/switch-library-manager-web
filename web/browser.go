package web

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"time"
)

// On a desktop (Windows, macOS) the program is usually started with a double click: the app
// opens in the browser once it answers, so nobody has to know its address. SLM_OPEN_BROWSER=false
// turns it off; servers and Docker never open anything.
func shouldOpenBrowser() bool {
	if value := os.Getenv("SLM_OPEN_BROWSER"); value != "" {
		return strings.EqualFold(value, "true")
	}
	return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
}

// openBrowserWhenReady waits for the server to answer, then opens its address.
func (web *Web) openBrowserWhenReady(port int) {
	address := fmt.Sprintf("127.0.0.1:%d", port)
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		connection, err := net.DialTimeout("tcp", address, time.Second)
		if err != nil {
			continue
		}
		connection.Close()
		url := fmt.Sprintf("http://localhost:%d/", port)
		web.sugarLogger.Infof("[Opening %s in the browser]", url)
		if err := openUrl(url); err != nil {
			web.sugarLogger.Infof("Open %s in your browser (%v)", url, err)
		}
		return
	}
}
