package web

import "os/exec"

// openUrl opens a web address with the default browser of macOS.
func openUrl(url string) error {
	return exec.Command("open", url).Start()
}
