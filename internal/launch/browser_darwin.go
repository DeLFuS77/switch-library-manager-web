package launch

import "os/exec"

// OpenUrl opens a web address with the default browser of macOS.
func OpenUrl(url string) error {
	return exec.Command("open", url).Start()
}
