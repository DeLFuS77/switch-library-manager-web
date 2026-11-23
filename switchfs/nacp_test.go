package switchfs

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"testing"
)

func TestExtractNacpBranches(t *testing.T) {
	reader := bytes.NewReader(nil)
	if _, err := ExtractNacp(&ContentMetaAttributes{}, reader, &PFS0{}, 0); err == nil {
		t.Fatal("expected missing control error")
	}
	cnmt := &ContentMetaAttributes{Contents: map[string]Content{
		"Control": {ID: "missing"},
	}}
	partition := &PFS0{Files: []fileEntry{{Name: "other", StartOffset: 0}}}
	if _, err := ExtractNacp(cnmt, reader, partition, 0); err == nil {
		t.Fatal("expected missing control NCA error")
	}

	section := makeRomfsSection(t, "control.nacp")
	nca := makeSyntheticNCA(t, section, 0)
	_, decoded, err := openMetaNcaDataSection(bytes.NewReader(nca), 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readRomfsHeader(decoded); err != nil {
		t.Fatalf("synthetic RomFS: %x: %v", decoded[:16], err)
	}
	control := &PFS0{Files: []fileEntry{{Name: "control-id", StartOffset: 0}}}
	got, err := ExtractNacp(&ContentMetaAttributes{Contents: map[string]Content{
		"Control": {ID: "control-id"},
	}}, bytes.NewReader(nca), control, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got.TitleName["AmericanEnglish"].Title != "Synthetic Game" {
		t.Fatalf("unexpected extracted NACP: %#v", got)
	}

	unsupported := makeSyntheticNCA(t, make([]byte, 0x200), 1)
	if _, err := ExtractNacp(&ContentMetaAttributes{Contents: map[string]Content{
		"Control": {ID: "control-id"},
	}}, bytes.NewReader(unsupported), control, 0); err == nil {
		t.Fatal("expected unsupported filesystem error")
	}

	noNacp := makeRomfsSection(t, "other")
	noNacpNca := makeSyntheticNCA(t, noNacp, 0)
	if _, err := ExtractNacp(&ContentMetaAttributes{Contents: map[string]Content{
		"Control": {ID: "control-id"},
	}}, bytes.NewReader(noNacpNca), control, 0); err == nil {
		t.Fatal("expected missing control.nacp error")
	}
}

// Control data that keeps its names compressed gives 32 entries of 0x300 bytes, a name and its
// publisher each, the first 16 in the same languages as always, then Polish and Thai: each
// language reads its own entry, with its publisher.
func TestReadNacpCompressedTitles(t *testing.T) {
	const nacpSize = 0x4000
	const titleDataSize = 32 * 0x300
	titleData := make([]byte, titleDataSize)
	entries := map[Language][2]string{
		AmericanEnglish:      {"Compressed Game", "Publisher"},
		BritishEnglish:       {"Compressed Game UK", "Publisher UK"},
		Japanese:             {"圧縮ゲーム", "パブリッシャー"},
		LatinAmericanSpanish: {"Juego comprimido LA", "Editora LA"},
		Spanish:              {"Juego comprimido", "Editora"},
		Korean:               {"압축 게임", "퍼블리셔"},
		BrazilianPortuguese:  {"Jogo comprimido", "Editora BR"},
		Polish:               {"Skompresowana gra", "Wydawca"},
		Thai:                 {"เกมบีบอัด", "ผู้จัดจำหน่าย"},
	}
	for language, entry := range entries {
		copy(titleData[int(language)*0x300:], entry[0])
		copy(titleData[int(language)*0x300+0x200:], entry[1])
	}
	// an entry past the languages the app knows is not read
	copy(titleData[20*0x300:], "Unknown language")
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(titleData); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if compressed.Len() > 0xffff {
		t.Fatalf("compressed title data is too large: %d", compressed.Len())
	}

	data := make([]byte, nacpSize)
	binary.LittleEndian.PutUint16(data, uint16(compressed.Len()))
	copy(data[2:], compressed.Bytes())
	data[0x3215] = 1
	nacp, err := readNacp(data, RomfsHeader{}, RomfsFileEntry{size: nacpSize})
	if err != nil {
		t.Fatal(err)
	}
	for language, entry := range entries {
		got := nacp.TitleName[language.String()]
		if got.Language != language || got.Title != entry[0] || got.Publisher != entry[1] {
			t.Errorf("%v: %+v, want %q by %q", language, got, entry[0], entry[1])
		}
	}
	if got := nacp.TitleName["Thai"].Language.ToLanguageTag(); got != "th" {
		t.Errorf("the tag of Thai: %q", got)
	}
	if got := nacp.TitleName["Polish"].Language.ToLanguageTag(); got != "pl" {
		t.Errorf("the tag of Polish: %q", got)
	}
	if len(nacp.TitleName) != int(knownLanguages) {
		t.Errorf("%d languages read, want %d", len(nacp.TitleName), knownLanguages)
	}
	if !nacp.CompressedNamesRead {
		t.Error("the names are not marked as read as 32 entries")
	}

	// the same data but its compressed names broken
	broken := append([]byte(nil), data...)
	binary.LittleEndian.PutUint16(broken, 0x3000)
	if _, err := readNacp(broken, RomfsHeader{}, RomfsFileEntry{size: nacpSize}); err == nil {
		t.Error("compressed names larger than their space were read")
	}
}

// Control data that does not keep its names compressed gives the 16 languages of always,
// without Polish and Thai.
func TestNacpTitleEntriesLegacy(t *testing.T) {
	data := make([]byte, 0x4000)
	copy(data[6*0x300:], "Juego")
	copy(data[6*0x300+0x200:], "Editora")
	titleData, entries, err := NacpTitleEntries(data)
	if err != nil || entries != 16 || len(titleData) != 0x3000 {
		t.Fatalf("%d entries in %#x bytes: %v", entries, len(titleData), err)
	}
	nacp, err := readNacp(data, RomfsHeader{}, RomfsFileEntry{size: uint64(len(data))})
	if err != nil {
		t.Fatal(err)
	}
	if got := nacp.TitleName["Spanish"]; got.Title != "Juego" || got.Publisher != "Editora" {
		t.Errorf("Spanish: %+v", got)
	}
	if _, ok := nacp.TitleName["Polish"]; ok || len(nacp.TitleName) != 16 {
		t.Errorf("%d languages read: %v", len(nacp.TitleName), nacp.TitleName)
	}
	if _, _, err := NacpTitleEntries(data[:0x2000]); err == nil {
		t.Error("a NACP too small was read")
	}
}

func makeRomfsSection(t *testing.T, name string) []byte {
	t.Helper()
	const (
		fileTableOffset = 0x50
		dataOffset      = 0x100
		nacpSize        = 0x3080
	)
	nameBytes := []byte(name)
	entrySize := 0x20 + len(nameBytes)*2
	sectionSize := dataOffset + nacpSize
	if name != "control.nacp" {
		sectionSize = dataOffset + 0x200
	}
	sectionSize = (sectionSize + 0x1ff) &^ 0x1ff
	data := make([]byte, sectionSize)
	putRomfsHeader(data, RomfsHeader{
		HeaderSize:          0x50,
		FileMetaTableOffset: fileTableOffset,
		FileMetaTableSize:   uint64(entrySize),
		DataOffset:          dataOffset,
	})
	entry := data[fileTableOffset:]
	binary.LittleEndian.PutUint32(entry[0x1c:0x20], uint32(len(nameBytes)*2))
	binary.LittleEndian.PutUint64(entry[0x8:0x10], 0)
	if name == "control.nacp" {
		binary.LittleEndian.PutUint64(entry[0x10:0x18], nacpSize)
	} else {
		binary.LittleEndian.PutUint64(entry[0x10:0x18], 0x200)
	}
	for i, value := range nameBytes {
		binary.LittleEndian.PutUint16(entry[0x20+i*2:], uint16(value))
	}
	if name == "control.nacp" {
		copy(data[dataOffset:], []byte("Synthetic Game"))
		copy(data[dataOffset+0x3060:], []byte("1.0.0"))
	}
	return data
}

// The game says it is a demo with bit 0 of its attributes, whatever its name.
func TestNacpDemoFlag(t *testing.T) {
	extract := func(demo bool) Nacp {
		section := makeRomfsSection(t, "control.nacp")
		if demo {
			start := bytes.Index(section, []byte("Synthetic Game"))
			section[start+0x3028] = 1
		}
		nca := makeSyntheticNCA(t, section, 0)
		control := &PFS0{Files: []fileEntry{{Name: "control-id", StartOffset: 0}}}
		got, err := ExtractNacp(&ContentMetaAttributes{Contents: map[string]Content{"Control": {ID: "control-id"}}}, bytes.NewReader(nca), control, 0)
		if err != nil {
			t.Fatal(err)
		}
		return *got
	}
	if got := extract(true); !got.Demo || !got.AttributesRead {
		t.Fatalf("a demo: %+v", got)
	}
	if got := extract(false); got.Demo || !got.AttributesRead {
		t.Fatalf("a game: %+v", got)
	}
}

// A game of the Chinese store names itself in Simplified Chinese only: the name is not lost
// under the empty Brazilian Portuguese one, the last of the sixteen.
func TestReadNacpKeepsEveryLanguage(t *testing.T) {
	const nacpSize = 0x3080
	data := make([]byte, nacpSize)
	copy(data[14*0x300:], []byte("疯狂兔子：奇遇派对 试玩版"))
	copy(data[13*0x300:], []byte("瘋狂兔子"))
	nacp, err := readNacp(data, RomfsHeader{}, RomfsFileEntry{size: nacpSize})
	if err != nil {
		t.Fatal(err)
	}
	if got := nacp.TitleName["Chinese"]; got.Title != "疯狂兔子：奇遇派对 试玩版" || got.Language.ToLanguageTag() != "zh-Hans" {
		t.Errorf("Simplified Chinese: %+v", got)
	}
	if got := nacp.TitleName["Taiwanese"]; got.Title != "瘋狂兔子" || got.Language.ToLanguageTag() != "zh-Hant" {
		t.Errorf("Traditional Chinese: %+v", got)
	}
	if got := nacp.TitleName["BrazilianPortuguese"]; got.Title != "" || got.Language.ToLanguageTag() != "pt-BR" {
		t.Errorf("Brazilian Portuguese: %+v", got)
	}
	if !nacp.NamesRead {
		t.Error("the names are marked as read apart")
	}
}
