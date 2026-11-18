//go:build !official

package settings

import "testing"

// A program built from the code without the tag "official", which only the workflows of this
// repository use, is an unofficial build (TRADEMARKS.md).
func TestBuildWithoutTheOfficialTag(t *testing.T) {
	if OFFICIAL_BUILD {
		t.Error("a build without the tag official is an official build")
	}
}
