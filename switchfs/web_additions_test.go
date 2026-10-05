package switchfs

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// Covers metadata used by the web API that the upstream port does not expose.

func makeCNMTWithExtendedHeader(titleID uint64, metaType byte, requiredVersion uint32, contentID [16]byte) []byte {
	const extendedHeaderSize = 0x10
	data := make([]byte, 0x20+extendedHeaderSize+0x38)
	binary.LittleEndian.PutUint64(data[0:8], titleID)
	data[0xC] = metaType
	binary.LittleEndian.PutUint16(data[0xE:0x10], extendedHeaderSize)
	binary.LittleEndian.PutUint16(data[0x10:0x12], 1)
	binary.LittleEndian.PutUint32(data[0x28:0x2C], requiredVersion)
	position := 0x20 + extendedHeaderSize
	copy(data[position+0x20:position+0x30], contentID[:])
	data[position+0x36] = 3 // Control
	return data
}

func TestReadBinaryCnmtRequiredTitleVersion(t *testing.T) {
	id := [16]byte{0xaa}
	cnmtData := makeCNMTWithExtendedHeader(0x0100000000010001, ContentMetaType_AddOnContent, 0x00050000, id)
	container := makePFS0(pfs0Magic, []string{"x.cnmt.nca"}, [][]byte{cnmtData})
	parsed, err := readPfs0(bytes.NewReader(container), 0)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := readBinaryCnmt(parsed, container)
	if err != nil {
		t.Fatal(err)
	}
	if meta.RequiredTitleVersion != 0x00050000 || meta.Type != "DLC" {
		t.Fatalf("unexpected metadata: %#v", meta)
	}
	if meta.Contents["Control"].ID != "aa000000000000000000000000000000" {
		t.Fatalf("content table not read after extended header: %#v", meta.Contents)
	}
}

func TestReadBinaryCnmtWithoutExtendedHeader(t *testing.T) {
	cnmtData := makeCNMT(0x0100000000010000, 1, ContentMetaType_Application, [16]byte{1})
	container := makePFS0(pfs0Magic, []string{"x.cnmt.nca"}, [][]byte{cnmtData})
	parsed, err := readPfs0(bytes.NewReader(container), 0)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := readBinaryCnmt(parsed, container)
	if err != nil {
		t.Fatal(err)
	}
	if meta.RequiredTitleVersion != 0 {
		t.Fatalf("expected no required version, got %d", meta.RequiredTitleVersion)
	}
}

func TestLanguageToLanguageTag(t *testing.T) {
	tests := map[Language]string{
		AmericanEnglish:      "en-US",
		LatinAmericanSpanish: "es-419",
		Spanish:              "es",
		Language(15):         "zh-Hans",
		Language(16):         "und",
		Language(-1):         "und",
	}
	for language, want := range tests {
		if got := language.ToLanguageTag(); got != want {
			t.Errorf("Language(%d).ToLanguageTag() = %q, want %q", int(language), got, want)
		}
	}
}
