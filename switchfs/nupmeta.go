package switchfs

import (
	"bytes"
	"fmt"
	"sort"
	"unicode/utf8"
)

// the types of the metadata the verification looks at besides the ones read for the library
const (
	nupTlvNpdm       = 0x0001
	nupTlvBuildID    = 0x0007
	nupTlvSourceDump = 0x0008
)

// checkNupMeta gives the metadata of the packages of a NUP that cannot be what it says, which
// no hash of the format covers: a build ID that is not 32 bytes, text that is not UTF-8,
// records of languages that run past their block. The types of the records are only sure in
// a block with an NPDM: the packer of the format counted them from 0 for a while, which a
// block without one does not show (a DLC has none), so such a block only has its records of
// languages looked at, which are alike either way.
func checkNupMeta(container *NupContainer) []NupDamaged {
	damaged := []NupDamaged{}
	for _, pkg := range container.Packages {
		label := nupLabel(pkg)
		bad := func(record string, reason string) {
			damaged = append(damaged, NupDamaged{Package: label, Path: "metadata: " + record, Reason: reason})
		}
		localized := map[uint16]map[string][]byte{}
		for _, kind := range []uint16{nupTlvLocalizedTitles, nupTlvLocalizedIcons} {
			if block, ok := pkg.Meta[kind]; ok {
				records, err := parseNupLocalized(block)
				if err != nil {
					bad("records of languages", err.Error())
				}
				localized[kind] = records
			}
		}
		if !bytes.HasPrefix(pkg.Meta[nupTlvNpdm], []byte("META")) {
			continue
		}
		if id, ok := pkg.Meta[nupTlvBuildID]; ok && len(id) != 32 {
			bad("build ID", fmt.Sprintf("it is %d bytes instead of 32", len(id)))
		}
		for _, text := range []struct {
			kind   uint16
			record string
		}{{nupTlvDisplayVersion, "display version"}, {nupTlvTitleName, "name"}, {nupTlvPublisher, "publisher"}, {nupTlvSourceDump, "source dump"}} {
			if value, ok := pkg.Meta[text.kind]; ok && !utf8.Valid(value) {
				bad(text.record, "it is not UTF-8 text")
			}
		}
		names := localized[nupTlvLocalizedTitles]
		tags := make([]string, 0, len(names))
		for tag := range names {
			tags = append(tags, tag)
		}
		sort.Strings(tags)
		for _, tag := range tags {
			if !utf8.Valid(names[tag]) {
				bad("names by language", "the name in "+tag+" is not UTF-8 text")
			}
		}
	}
	return damaged
}
