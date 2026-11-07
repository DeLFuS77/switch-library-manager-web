package switchfs

import (
	"context"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

func TestVerifyNupLooksAtTheMetadata(t *testing.T) {
	npdm := append([]byte("META"), make([]byte, 0x80)...)
	packages := []testnup.Package{
		// counted from 0 with an NPDM (type 0): its build ID (6) is read as 0x0007, 31 bytes, and
		// the rest one up too. A record 0x0007 that is not 32 bytes makes a block without type 0
		// read as counted from 0, so a damaged build ID shows in a block that has one
		{TitleId: 0x0400486024DEC000, Kind: testnup.Base,
			Meta: []testnup.Record{
				{Type: 0x0000, Payload: npdm},
				{Type: 0x0006, Payload: make([]byte, 31)},
				{Type: 0x0004, Payload: []byte{0xFF, 0xFE, 'x'}},
				{Type: 0x0008, Payload: []byte{2, 'j', 'a', 9, 0, 0, 0, 'x'}},
			},
			Files: []testnup.File{{Path: "exefs/main.npdm", Data: npdm}}},
		// a DLC of a packer that counted the types from 0: its source dump (7) reads as a build
		// ID, its build ID (6) as a publisher, which are not looked at without an NPDM
		{TitleId: 0x0400486024DED001, Kind: testnup.Addon,
			Meta: []testnup.Record{
				{Type: 0x0007, Payload: []byte("dlc folder")},
				{Type: 0x0006, Payload: []byte{0xFF, 0xFE, 0xFD}},
			},
			Files: []testnup.File{{Path: "romfs/dlc.bin", Data: []byte("dlc")}}},
	}
	_, err := VerifyFile(context.Background(), writeNup(t, testnup.Build(packages)), nil)
	found := damagedNup(t, err)
	for _, want := range []string{"game v0/metadata: build ID", "game v0/metadata: name", "game v0/metadata: records of languages"} {
		if _, ok := found[want]; !ok {
			t.Errorf("%q not found in %v", want, found)
		}
	}
	if len(found) != 3 {
		t.Errorf("damaged %v", found)
	}

	// the same NUP with its metadata whole is good
	packages[0].Meta = []testnup.Record{{Type: 0x0001, Payload: npdm}, {Type: 0x0007, Payload: make([]byte, 32)}, {Type: 0x0005, Payload: []byte("Made-up Quest")}}
	if check, err := VerifyFile(context.Background(), writeNup(t, testnup.Build(packages)), nil); err != nil || check.Partial || len(check.Modified) > 0 {
		t.Errorf("%+v %v", check, err)
	}
}
