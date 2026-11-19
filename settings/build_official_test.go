//go:build official

package settings

import "testing"

// The programs of the workflows of this repository, built with the tag "official", are official
// builds (TRADEMARKS.md).
func TestBuildWithTheOfficialTag(t *testing.T) {
	if !OFFICIAL_BUILD {
		t.Error("a build with the tag official is an unofficial build")
	}
}
