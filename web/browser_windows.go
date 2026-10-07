package web

import "os/exec"

// openUrl opens a web address with the default browser of Windows.
func openUrl(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
