package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// syntheticTitle writes the package of one title: its Meta NCA with a CNMT listing the
// contents, the contents, and a ticket and a certificate named after its rights ID.
func syntheticTitle(t *testing.T, folder string, titleId uint64, metaType byte, version uint32, contents map[[16]byte][]byte, kinds map[[16]byte]byte, extension string) string {
	t.Helper()
	ids := [][16]byte{}
	for id := range contents {
		ids = append(ids, id)
	}
	// in a fixed order
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if bytes.Compare(ids[j][:], ids[i][:]) < 0 {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	cnmt := make([]byte, 0x20+len(ids)*0x38)
	binary.LittleEndian.PutUint64(cnmt[0:8], titleId)
	binary.LittleEndian.PutUint32(cnmt[8:12], version)
	cnmt[0xC] = metaType
	binary.LittleEndian.PutUint16(cnmt[0x10:0x12], uint16(len(ids)))
	for i, id := range ids {
		entry := cnmt[0x20+i*0x38:]
		copy(entry[0x20:0x30], id[:])
		entry[0x36] = kinds[id]
	}
	meta := makeSyntheticNCA(t, pad512(makePFS0(pfs0Magic, []string{"Title.cnmt"}, [][]byte{cnmt})), 1)
	metaId := sha256Sum(meta)
	metaName := hex.EncodeToString(metaId[:16])

	rights := strconv.FormatUint(titleId, 16)
	for len(rights) < 16 {
		rights = "0" + rights
	}
	rights += "0000000000000004"
	names := []string{metaName + ".cnmt.nca"}
	payloads := [][]byte{meta}
	for _, id := range ids {
		name := hex.EncodeToString(id[:]) + ".nca"
		if extension == ".nsz" && kinds[id] == 1 {
			name = hex.EncodeToString(id[:]) + ".ncz"
		}
		names = append(names, name)
		payloads = append(payloads, contents[id])
	}
	names = append(names, metaName+".cnmt.xml", rights+".tik", rights+".cert")
	payloads = append(payloads, []byte("<ContentMeta/>"), []byte("ticket of "+rights), []byte("cert of "+rights))
	path := filepath.Join(folder, strconv.FormatUint(titleId, 16)+extension)
	if err := os.WriteFile(path, makePFS0(pfs0Magic, names, payloads), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// entriesOf gives the files of a package, by name, in their order.
func entriesOf(t *testing.T, path string) ([]string, map[string][]byte) {
	t.Helper()
	reader, err := OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	data := map[string][]byte{}
	for _, file := range pkg.files {
		content := make([]byte, file.size)
		if err := readAtFull(reader, content, file.offset); err != nil {
			t.Fatal(err)
		}
		names = append(names, file.name)
		data[file.name] = content
	}
	return names, data
}

func splitFixture(t *testing.T, extension string) (folder string, originals []string, super string) {
	folder = t.TempDir()
	program, control := [16]byte{0x9a, 1}, [16]byte{0xc0, 1}
	patch, delta := [16]byte{0x9a, 2}, [16]byte{0xde, 2}
	data := [16]byte{0xda, 3}
	base := syntheticTitle(t, folder, 0x0100000000010000, ContentMetaType_Application, 0,
		map[[16]byte][]byte{program: []byte("program of the game"), control: []byte("control")}, map[[16]byte]byte{program: 1, control: 3}, extension)
	update := syntheticTitle(t, folder, 0x0100000000010800, ContentMetaType_Patch, 131072,
		map[[16]byte][]byte{patch: []byte("program of the update"), delta: []byte("delta fragment")}, map[[16]byte]byte{patch: 1, delta: 6}, extension)
	dlc := syntheticTitle(t, folder, 0x0100000000011001, ContentMetaType_AddOnContent, 0,
		map[[16]byte][]byte{data: []byte("data of the DLC")}, map[[16]byte]byte{data: 2}, extension)
	originals = []string{base, update, dlc}
	super = filepath.Join(folder, "super"+extension)
	if _, err := MergePackages(context.Background(), originals, super, false, nil); err != nil {
		t.Fatal(err)
	}
	return folder, originals, super
}

// A SuperNSP splits back into the packages it was made of: the same files, with the same
// bytes, in the same order; merged again, the parts give the SuperNSP byte for byte.
func TestSplitGivesBackTheOriginalPackages(t *testing.T) {
	for _, extension := range []string{".nsp", ".nsz"} {
		t.Run(extension, func(t *testing.T) {
			folder, originals, super := splitFixture(t, extension)
			plan, err := PlanSplit(super)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Parts) != 3 || !plan.Complete() || len(plan.Unclaimed) != 0 {
				t.Fatalf("three parts, nothing left: %+v", plan)
			}
			wantTypes := []string{"BASE", "UPD", "DLC"}
			parts := []string{}
			for i, part := range plan.Parts {
				if part.Type != wantTypes[i] {
					t.Fatalf("part %d is %s", i, part.Type)
				}
				if part.Compressed != (extension == ".nsz" && part.Type != "DLC") {
					t.Fatalf("part %s compressed: %v", part.Type, part.Compressed)
				}
				target := filepath.Join(folder, "part"+strconv.Itoa(i)+extension)
				var last, total int64
				if err := WriteSplitPart(context.Background(), super, part, target, func(done, all int64) { last, total = done, all }); err != nil {
					t.Fatal(err)
				}
				if last != total || total == 0 {
					t.Fatalf("progress %d of %d", last, total)
				}
				if info, _ := os.Stat(target); info.Size() != part.Size {
					t.Fatalf("size %d, planned %d", info.Size(), part.Size)
				}
				wantNames, wantData := entriesOf(t, originals[i])
				gotNames, gotData := entriesOf(t, target)
				if len(gotNames) != len(wantNames) {
					t.Fatalf("part %s: %v, want %v", part.Type, gotNames, wantNames)
				}
				for j, name := range wantNames {
					if gotNames[j] != name || !bytes.Equal(gotData[name], wantData[name]) {
						t.Fatalf("part %s: %s differs", part.Type, name)
					}
				}
				parts = append(parts, target)
			}
			// the update keeps its delta fragment: the part is the update as it was
			if names, _ := entriesOf(t, parts[1]); len(names) != 6 {
				t.Fatalf("the update with its delta fragment: %v", names)
			}

			again := filepath.Join(folder, "again"+extension)
			if _, err := MergePackages(context.Background(), parts, again, false, nil); err != nil {
				t.Fatal(err)
			}
			want, _ := os.ReadFile(super)
			got, _ := os.ReadFile(again)
			if !bytes.Equal(got, want) {
				t.Fatal("the parts merged again are not the SuperNSP")
			}

			// the scan reads each part as one title
			for i, part := range parts {
				contents, err := ReadNspMetadata(part)
				if err != nil || len(contents) != 1 || contents[plan.Parts[i].TitleId] == nil {
					t.Fatalf("part %d: %v %v", i, contents, err)
				}
			}
		})
	}
}

func TestSplitNeedsSeveralTitles(t *testing.T) {
	_, originals, _ := splitFixture(t, ".nsp")
	if _, err := PlanSplit(originals[0]); !errors.Is(err, ErrSingleTitle) {
		t.Fatalf("one title: %v", err)
	}
	broken := filepath.Join(t.TempDir(), "broken.nsp")
	os.WriteFile(broken, []byte("not a package"), 0o644)
	if _, err := PlanSplit(broken); err == nil {
		t.Fatal("a broken file")
	}
}

// An NCA no CNMT lists is left out of the parts: the original has more than its parts.
func TestSplitReportsUnclaimedFiles(t *testing.T) {
	folder, originals, _ := splitFixture(t, ".nsp")
	stray := filepath.Join(folder, "stray.nsp")
	os.WriteFile(stray, makePFS0(pfs0Magic, []string{"0123456789abcdef0123456789abcdef.nca"}, [][]byte{[]byte("nobody lists me")}), 0o644)
	super := filepath.Join(folder, "with-stray.nsp")
	if _, err := MergePackages(context.Background(), append(originals, stray), super, false, nil); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSplit(super)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Complete() || len(plan.Unclaimed) != 1 {
		t.Fatalf("the stray NCA: %v", plan.Unclaimed)
	}

	// a part that cannot be written leaves nothing behind
	target := filepath.Join(folder, "missing", "part.nsp")
	if err := WriteSplitPart(context.Background(), super, plan.Parts[0], target, nil); err == nil {
		t.Fatal("no folder to write in")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target = filepath.Join(folder, "cancelled.nsp")
	if err := WriteSplitPart(ctx, super, plan.Parts[0], target, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("no part left behind")
	}
}

// writeCard writes a game card dump whose secure partition holds the files of packages.
func writeCard(t *testing.T, path string, packages []string) {
	t.Helper()
	names := []string{}
	payloads := [][]byte{}
	for _, pkg := range packages {
		pkgNames, data := entriesOf(t, pkg)
		for _, name := range pkgNames {
			names = append(names, name)
			payloads = append(payloads, data[name])
		}
	}
	secure := makePFS0(hfs0Magic, names, payloads)
	update := makePFS0(hfs0Magic, []string{"firmware.nca"}, [][]byte{bytes.Repeat([]byte{7}, 0x400)})
	root := makePFS0(hfs0Magic, []string{"update", "normal", "secure"}, [][]byte{update, makePFS0(hfs0Magic, nil, nil), secure})
	header := make([]byte, 0xF000)
	copy(header[0x100:], "HEAD")
	binary.LittleEndian.PutUint64(header[0x130:], 0xF000)
	if err := os.WriteFile(path, append(header, root...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A game card with several games splits into one card per game, with its update and DLC:
// the files of each game as they were, under the header of the original card.
func TestSplitCartridgeByGame(t *testing.T) {
	folder := t.TempDir()
	one, two := [16]byte{0xa1}, [16]byte{0xa2}
	three, four := [16]byte{0xb1}, [16]byte{0xb2}
	gameA := syntheticTitle(t, folder, 0x0100000000010000, ContentMetaType_Application, 0, map[[16]byte][]byte{one: []byte("game A")}, map[[16]byte]byte{one: 1}, ".nsp")
	updateA := syntheticTitle(t, folder, 0x0100000000010800, ContentMetaType_Patch, 65536, map[[16]byte][]byte{two: []byte("update A")}, map[[16]byte]byte{two: 1}, ".nsp")
	gameB := syntheticTitle(t, folder, 0x0100000000020000, ContentMetaType_Application, 0, map[[16]byte][]byte{three: []byte("game B")}, map[[16]byte]byte{three: 1}, ".nsp")
	dlcB := syntheticTitle(t, folder, 0x0100000000021001, ContentMetaType_AddOnContent, 0, map[[16]byte][]byte{four: []byte("DLC B")}, map[[16]byte]byte{four: 2}, ".nsp")
	card := filepath.Join(folder, "Collection.xci")
	// mixed, as a card may hold them
	writeCard(t, card, []string{gameB, gameA, dlcB, updateA})

	plan, err := PlanSplit(card)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Parts) != 2 || !plan.Complete() {
		t.Fatalf("two games: %+v", plan)
	}
	want := []struct {
		id       string
		version  int
		packages []string
	}{{"0100000000010000", 65536, []string{gameA, updateA}}, {"0100000000020000", 0, []string{gameB, dlcB}}}
	cardBytes, _ := os.ReadFile(card)
	for i, part := range plan.Parts {
		if !part.Cartridge || part.TitleId != want[i].id || part.Type != "BASE" || part.Version != want[i].version || part.Titles != 2 {
			t.Fatalf("part %d: %+v", i, part)
		}
		target := filepath.Join(folder, "part"+strconv.Itoa(i)+".xci")
		if err := WriteSplitPart(context.Background(), card, part, target, nil); err != nil {
			t.Fatal(err)
		}
		if info, _ := os.Stat(target); info.Size() != part.Size {
			t.Fatalf("size %d, planned %d", info.Size(), part.Size)
		}
		written, _ := os.ReadFile(target)
		if !bytes.Equal(written[:0xF000], cardBytes[:0xF000]) {
			t.Fatal("the header of the card is kept")
		}
		_, got := entriesOf(t, target)
		count := 0
		for _, pkg := range want[i].packages {
			names, data := entriesOf(t, pkg)
			for _, name := range names {
				count++
				if !bytes.Equal(got[name], data[name]) {
					t.Fatalf("part %d: %s differs", i, name)
				}
			}
		}
		if len(got) != count {
			t.Fatalf("part %d holds its game only: %d files, want %d", i, len(got), count)
		}
		// one game: nothing more to split
		if _, err := PlanSplit(target); !errors.Is(err, ErrSingleTitle) {
			t.Fatalf("part %d split again: %v", i, err)
		}
	}
	// a card with one game and its update is not split
	single := filepath.Join(folder, "Game.xci")
	writeCard(t, single, []string{gameA, updateA})
	if _, err := PlanSplit(single); !errors.Is(err, ErrSingleTitle) {
		t.Fatalf("one game: %v", err)
	}
}
