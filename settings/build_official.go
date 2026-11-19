//go:build official

package settings

// OFFICIAL_BUILD tells the program was built by the workflows of this repository (the release and
// the Docker image), the only builds with the tag "official" (TRADEMARKS.md). A program built from
// the code in any other way is an unofficial build (build_other.go).
const OFFICIAL_BUILD = true
