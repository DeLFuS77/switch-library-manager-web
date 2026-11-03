package launch

import "os/exec"

// OpenUrl opens a web address with the default browser of Windows.
func OpenUrl(url string) error {
	return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}
