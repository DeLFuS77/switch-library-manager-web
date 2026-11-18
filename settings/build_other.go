//go:build !official

package settings

// A program built from the code without the tag "official", which only the workflows of this
// repository use (the release and the Docker image): an unofficial build, not one of the versions
// published there (TRADEMARKS.md). The pages are told so (GlobalPageData.OfficialBuild).
const OFFICIAL_BUILD = false
