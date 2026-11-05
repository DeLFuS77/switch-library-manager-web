package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// nupNoise is data that zstd cannot make smaller, of a size.
func nupNoise(size int, seed int64) []byte {
	data := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(data)
	return data
}

// nupGame is a game with an update and a DLC, files raw and with zstd, one of them bigger than
// what is read at once, and a file the update shares with the game.
func nupGame() []testnup.Package {
	shared := bytes.Repeat([]byte("shared data of the game "), 4000)
	return []testnup.Package{
		{TitleId: 0x0400486024DEC000, Kind: testnup.Base, Version: 65536,
			Meta: []testnup.Record{{Type: testnup.TitleName, Payload: []byte("Made-up Quest")}},
			Files: []testnup.File{
				{Path: "exefs/main", Data: nupNoise(3000, 1)},
				{Path: "exefs/main.npdm", Data: []byte("NPDM made up")},
				{Path: "romfs/Data/big.bin", Data: bytes.Repeat([]byte("0123456789abcdef"), 300_000), Zstd: true},
				{Path: "romfs/Data/noise.bin", Data: nupNoise(5<<20/2, 2)},
				{Path: "romfs/Data/shared.bin", Data: shared, Zstd: true},
				{Path: "romfs/empty.txt", Data: nil},
				{Path: "romfs/Sound/a/b/c/deep.bin", Data: []byte("deep"), Zstd: true},
			}},
		{TitleId: 0x0400486024DEC000, Kind: testnup.Update, Version: 131072,
			Files: []testnup.File{
				{Path: "exefs/main", Data: nupNoise(3200, 3)},
				{Path: "romfs/Data/shared.bin", Data: shared, Zstd: true},
				{Path: "romfs/Data/patch.bin", Data: []byte("patched"), Zstd: true},
			}},
		{TitleId: 0x0400486024DED001, Kind: testnup.Addon, Version: 0,
			Files: []testnup.File{{Path: "romfs/dlc.bin", Data: nupNoise(100, 4)}}},
	}
}

// damagedNup gives the damaged files a check of a NUP found, by package and path, and fails the
// test on any other error.
func damagedNup(t *testing.T, err error) map[string]string {
	t.Helper()
	var damaged *NupDamagedError
	if !errors.As(err, &damaged) {
		t.Fatalf("not damaged: %v", err)
	}
	found := map[string]string{}
	for _, d := range damaged.Damaged {
		found[d.Package+"/"+d.Path] = d.Reason
	}
	return found
}

func TestVerifyNupGood(t *testing.T) {
	for _, o := range []testnup.Options{{Dedup: true}, {}, {TagFiles: true, Dedup: true}} {
		for _, threads := range []int{1, 4} {
			path := writeNup(t, testnup.BuildWith(nupGame(), o))
			calls, last, size := 0, int64(0), int64(0)
			check, err := VerifyFile(WithThreads(context.Background(), threads), path, func(done int64, total int64) {
				calls++
				if done > total || done < 0 {
					t.Errorf("progress %d of %d", done, total)
				}
				last, size = done, total
			})
			if err != nil {
				t.Fatalf("%+v, %d threads: %v", o, threads, err)
			}
			if check.Partial || len(check.Modified) > 0 || calls < 2 || last != size || size == 0 {
				t.Errorf("%+v: %+v, %d calls, %d of %d", o, check, calls, last, size)
			}
		}
	}
}

// damageNup flips a byte of the data of a file.
func damageNup(nup []byte, packages []testnup.Package, pkg int, path string, at int) {
	offset, size := testnup.Extent(nup, packages, pkg, path)
	nup[offset+min(at, size-1)] ^= 0x55
}

func TestVerifyNupFindsDamagedFiles(t *testing.T) {
	packages := nupGame()
	nup := testnup.Build(packages)
	damageNup(nup, packages, 0, "romfs/Data/noise.bin", 1<<20+7)
	damageNup(nup, packages, 0, "exefs/main", 0)
	// the data the update shares with the game: damaged in both
	damageNup(nup, packages, 0, "romfs/Data/shared.bin", 40)
	// zstd that cannot be decompressed any more
	damageNup(nup, packages, 0, "romfs/Data/big.bin", 2)
	_, err := VerifyFile(WithThreads(context.Background(), 2), writeNup(t, nup), nil)
	found := damagedNup(t, err)
	want := []string{"game v65536/romfs/Data/noise.bin", "game v65536/exefs/main", "game v65536/romfs/Data/shared.bin",
		"update v131072/romfs/Data/shared.bin", "game v65536/romfs/Data/big.bin"}
	if len(found) != len(want) {
		t.Errorf("damaged %v", found)
	}
	for _, path := range want {
		if found[path] == "" {
			t.Errorf("%s not found damaged: %v", path, found)
		}
	}
	if found["game v65536/exefs/main"] != "its SHA-256 does not match" {
		t.Errorf("raw reason %q", found["game v65536/exefs/main"])
	}
	// the error tells the first, and how many more
	if message := err.Error(); !strings.HasPrefix(message, "game v65536/") || !strings.HasSuffix(message, "(and 4 more damaged files)") {
		t.Errorf("message %q", message)
	}
}

func TestVerifyNupOneDamagedFile(t *testing.T) {
	packages := nupGame()
	nup := testnup.Build(packages)
	damageNup(nup, packages, 2, "romfs/dlc.bin", 5)
	_, err := VerifyFile(context.Background(), writeNup(t, nup), nil)
	if err == nil || err.Error() != "DLC 0400486024DED001 v0/romfs/dlc.bin: its SHA-256 does not match" {
		t.Errorf("err %v", err)
	}
}

// setNupFile changes the record of a file in the table of contents, and writes its hash again.
func setNupFile(nup []byte, packages int, index int, change func(record []byte)) {
	le := binary.LittleEndian
	toc := nup[nupHeaderSize:]
	dirs := int(le.Uint64(toc[0x08:]))
	change(toc[nupTocHeadSize+packages*nupPackageSize+dirs*nupDirSize+index*nupFileSize:][:nupFileSize])
	testnup.Rehash(nup)
}

func TestVerifyNupFindsZstdOfTheWrongSize(t *testing.T) {
	packages := []testnup.Package{{TitleId: 0x0100000000010000, Files: []testnup.File{
		{Path: "romfs/a.bin", Data: bytes.Repeat([]byte("a"), 5000), Zstd: true}}}}
	for _, size := range []uint64{4000, 6000} {
		nup := testnup.Build(packages)
		setNupFile(nup, 1, 0, func(record []byte) { binary.LittleEndian.PutUint64(record[0x08:], size) })
		_, err := VerifyFile(context.Background(), writeNup(t, nup), nil)
		found := damagedNup(t, err)
		if len(found) != 1 || !strings.Contains(found["game v0/romfs/a.bin"], "decompresses to") {
			t.Errorf("size %d: %v", size, found)
		}
	}
}

func TestVerifyNupRefusesADamagedTable(t *testing.T) {
	nup := testnup.Build(nupGame())
	nup[nupHeaderSize+0x200] ^= 1
	_, err := VerifyFile(context.Background(), writeNup(t, nup), nil)
	if err == nil || !strings.Contains(err.Error(), "table of contents is damaged") {
		t.Errorf("err %v", err)
	}
}

func TestVerifyNupTellsAFileCutShort(t *testing.T) {
	nup := testnup.Build(nupGame())
	for _, cut := range []int{len(nup) - 1, len(nup) / 2, 0x90, 10} {
		_, err := VerifyFile(context.Background(), writeNup(t, nup[:cut]), nil)
		if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("cut at %d: %v", cut, err)
		}
	}
}

func TestVerifyNupStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := VerifyFile(WithThreads(ctx, 2), writeNup(t, testnup.Build(nupGame())), func(done int64, total int64) {
		if done > 0 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
}

// A file that cannot be read is no damaged file: the error of the read is told as it is, so the
// library leaves the file unchecked.
func TestVerifyNupTellsAReadThatFailed(t *testing.T) {
	path := writeNup(t, testnup.Build(nupGame()))
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	container, err := readNup(file, func() int64 { info, _ := file.Stat(); return info.Size() }())
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	// closed: every read fails as a disk that went away does
	jobs := []*nupExtent{{offset: container.Files[0].ExtentOffset, stored: container.Files[0].ExtentSize, size: container.Files[0].Size}}
	err = readNupExtents(context.Background(), file, jobs, 1, func(int64) {})
	var pathError *fs.PathError
	if !errors.As(err, &pathError) {
		t.Errorf("err %v", err)
	}
}

func TestReadNupRefusesMalformedTables(t *testing.T) {
	cases := map[string]func(nup []byte){
		"codec 7": func(nup []byte) { setNupFile(nup, 3, 0, func(record []byte) { record[0x40] = 7 }) },
		"data outside the file": func(nup []byte) {
			setNupFile(nup, 3, 0, func(record []byte) { binary.LittleEndian.PutUint64(record[0x10:], uint64(len(nup))) })
		},
		"data far outside the file": func(nup []byte) {
			setNupFile(nup, 3, 0, func(record []byte) { binary.LittleEndian.PutUint64(record[0x10:], 1<<63) })
		},
		"data not aligned": func(nup []byte) {
			setNupFile(nup, 3, 0, func(record []byte) {
				binary.LittleEndian.PutUint64(record[0x10:], binary.LittleEndian.Uint64(record[0x10:])+1)
			})
		},
		"raw of another size": func(nup []byte) {
			setNupFile(nup, 3, 0, func(record []byte) { binary.LittleEndian.PutUint64(record[0x18:], 1) })
		},
		"a name outside the pool": func(nup []byte) {
			setNupFile(nup, 3, 0, func(record []byte) { binary.LittleEndian.PutUint32(record[0x00:], 0xFFFFFF) })
		},
		"a child that is not there": func(nup []byte) {
			le := binary.LittleEndian
			toc := nup[nupHeaderSize:]
			pool := le.Uint64(toc[0x18:])
			le.PutUint32(toc[pool-4:], 0x7FFFFFFF)
			testnup.Rehash(nup)
		},
		"a root outside": func(nup []byte) {
			binary.LittleEndian.PutUint32(nup[nupHeaderSize+nupTocHeadSize+0x08:], 0xFFFF)
			testnup.Rehash(nup)
		},
		"a RomFS outside": func(nup []byte) {
			binary.LittleEndian.PutUint32(nup[nupHeaderSize+nupTocHeadSize+0x10:], 0xFFFF)
			testnup.Rehash(nup)
		},
	}
	for name, change := range cases {
		nup := testnup.Build(nupGame())
		change(nup)
		if _, err := readNup(bytes.NewReader(nup), int64(len(nup))); err == nil {
			t.Errorf("%s: read", name)
		}
	}
}

func TestVerifyNupRefusesALoop(t *testing.T) {
	nup := testnup.Build(nupGame())
	// the first child of the root (exefs) made the root itself
	le := binary.LittleEndian
	toc := nup[nupHeaderSize:]
	dirs, files := le.Uint64(toc[0x08:]), le.Uint64(toc[0x10:])
	children := nupTocHeadSize + 3*nupPackageSize + dirs*nupDirSize + files*nupFileSize
	le.PutUint32(toc[children:], 0)
	testnup.Rehash(nup)
	container, err := readNup(bytes.NewReader(nup), int64(len(nup)))
	if err != nil {
		t.Fatal(err)
	}
	if err := container.Walk(0, func(string, int) error { return nil }); err == nil || !strings.Contains(err.Error(), "inside itself") {
		t.Errorf("err %v", err)
	}
	if _, err := VerifyFile(context.Background(), writeNup(t, nup), nil); err == nil {
		t.Error("verified")
	}
}

func TestVerifyNupEmptyPackageAndEmptyFile(t *testing.T) {
	nup := testnup.Build([]testnup.Package{{TitleId: 1, Kind: testnup.Update, Flags: 1, Files: []testnup.File{{Path: "romfs/zero"}}}})
	if _, err := VerifyFile(WithThreads(context.Background(), 4), writeNup(t, nup), nil); err != nil {
		t.Fatal(err)
	}
	// an empty file whose hash is not the one of nothing
	setNupFile(nup, 1, 0, func(record []byte) { record[0x20] ^= 1 })
	_, err := VerifyFile(context.Background(), writeNup(t, nup), nil)
	if found := damagedNup(t, err); len(found) != 1 {
		t.Errorf("%v", found)
	}
	// a NUP of packages without files has nothing to read
	if _, err := VerifyFile(context.Background(), writeNup(t, testnup.Build([]testnup.Package{{TitleId: 1}})), nil); err != nil {
		t.Errorf("err %v", err)
	}
}

// Many small files, more than one read of the window holds, and files big enough to be read on
// their own, raw and with zstd: every one is checked, and a damaged one found wherever it is.
func TestVerifyNupManySmallAndBigFiles(t *testing.T) {
	files := []testnup.File{}
	for i := range 3000 {
		files = append(files, testnup.File{Path: "romfs/small/" + strings.Repeat("x", i%7) + string(rune('a'+i%26)) + "/" + strconv.Itoa(i),
			Data: nupNoise(4000+i%900, int64(i)), Zstd: i%3 == 0})
	}
	files = append(files, testnup.File{Path: "romfs/big/raw.bin", Data: nupNoise(9<<20, 99)},
		testnup.File{Path: "romfs/big/zstd.bin", Data: append(nupNoise(5<<20, 98), bytes.Repeat([]byte("z"), 4<<20)...), Zstd: true})
	packages := []testnup.Package{{TitleId: 0x0100000000010000, Files: files}}
	nup := testnup.Build(packages)
	path := writeNup(t, nup)
	for _, threads := range []int{1, 3, 8} {
		if _, err := VerifyFile(WithThreads(context.Background(), threads), path, nil); err != nil {
			t.Fatalf("%d threads: %v", threads, err)
		}
	}
	for _, path := range []string{"romfs/big/raw.bin", "romfs/big/zstd.bin", files[1500].Path, files[2999].Path} {
		damaged := bytes.Clone(nup)
		offset, size := testnup.Extent(damaged, packages, 0, path)
		damaged[offset+size-1] ^= 0x80
		_, err := VerifyFile(WithThreads(context.Background(), 4), writeNup(t, damaged), nil)
		if found := damagedNup(t, err); len(found) != 1 || found["game v0/"+path] == "" {
			t.Errorf("%s: %v", path, found)
		}
	}
}
