package switchfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// nupZeroBased is a game and its update whose metadata a build of the packer of the format wrote
// with its types counted from 0, and a DLC without metadata.
func nupZeroBased() []testnup.Package {
	packages := nupPlayable()
	packages[0].Meta = []testnup.Record{tlv(0, []byte("NPDM made up")), tlv(2, testnup.Jpeg), tlv(3, []byte("1.0.0")),
		tlv(4, []byte("Made-up Quest")), tlv(5, []byte("Nobody Games")), tlv(7, []byte("Made-up Dump"))}
	packages[1].Meta = []testnup.Record{tlv(0, []byte("NPDM of the update")), tlv(3, []byte("1.0.2"))}
	return packages
}

func TestFixNupZeroBased(t *testing.T) {
	path := writeNup(t, testnup.Build(nupZeroBased()))
	before, err := ReadNup(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.Emulators().ZeroBased || string(before.Packages[0].Meta[nupTlvTitleName]) != "Made-up Quest" {
		t.Fatalf("%+v", before.Packages[0])
	}
	beforeMeta, _ := ReadNupMetadata(path)
	fixed, err := FixNupZeroBased(path)
	if err != nil || fixed != 2 {
		t.Fatalf("%d %v", fixed, err)
	}
	after, err := ReadNup(path)
	if err != nil {
		t.Fatal(err)
	}
	game := after.Packages[0]
	if emulators := after.Emulators(); emulators.ZeroBased || !emulators.Ok() || game.ZeroBased || after.Packages[1].ZeroBased {
		t.Errorf("still counted from 0: %+v", emulators)
	}
	// as the emulators read it: each record where the format puts it, as written
	raw, _ := splitNupTlvs(readMetaBlock(t, path, game))
	if string(raw[nupTlvTitleName]) != "Made-up Quest" || string(raw[nupTlvPublisher]) != "Nobody Games" ||
		string(raw[nupTlvDisplayVersion]) != "1.0.0" || !bytes.Equal(raw[nupTlvIcon], testnup.Jpeg) || raw[0] != nil {
		t.Errorf("records %v", raw)
	}
	// the library reads the same name, publisher, version and icon
	afterMeta, _ := ReadNupMetadata(path)
	for id, meta := range beforeMeta {
		english := Language(AmericanEnglish).String()
		if afterMeta[id] == nil || (meta.Ncap != nil && (afterMeta[id].Ncap == nil || afterMeta[id].Ncap.TitleName[english] != meta.Ncap.TitleName[english] ||
			afterMeta[id].Ncap.DisplayVersion != meta.Ncap.DisplayVersion)) {
			t.Errorf("%s reads otherwise: %+v", id, afterMeta[id])
		}
	}
	if icon, err := after.Icon(); err != nil || !bytes.Equal(icon, testnup.Jpeg) {
		t.Errorf("icon: %v", err)
	}
	// the files are as they were, and the copy beside it is gone
	if check, err := VerifyNup(context.Background(), path, nil); err != nil || len(check.Modified) != 0 {
		t.Errorf("%v %+v", err, check)
	}
	if _, err := os.Stat(NupMetaBackup(path)); !os.IsNotExist(err) {
		t.Errorf("backup left: %v", err)
	}
	// again: nothing to do
	if fixed, err := FixNupZeroBased(path); err != nil || fixed != 0 {
		t.Errorf("again: %d %v", fixed, err)
	}
	// a NUP counted from 1 is left as it is
	good := writeNup(t, testnup.Build(nupPlayable()))
	original, _ := os.ReadFile(good)
	if fixed, err := FixNupZeroBased(good); err != nil || fixed != 0 {
		t.Errorf("counted from 1: %d %v", fixed, err)
	}
	if now, _ := os.ReadFile(good); !bytes.Equal(now, original) {
		t.Error("a NUP counted from 1 was written")
	}
}

// readMetaBlock is the block of metadata of a package as the file has it.
func readMetaBlock(t *testing.T, path string, pkg NupPackage) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data[pkg.metaOffset : pkg.metaOffset+pkg.metaSize]
}

// A copy left beside a NUP by a fix that stopped is put back first; the copy of another NUP stops
// the work and is left where it is.
func TestFixNupZeroBasedAfterAStop(t *testing.T) {
	original := testnup.Build(nupZeroBased())
	path := writeNup(t, original)
	if _, err := FixNupZeroBased(path); err != nil {
		t.Fatal(err)
	}
	fixed, _ := os.ReadFile(path)
	// the copy a fix that stopped after writing the first block leaves: the blocks as they were
	container, err := readNupBytes(original)
	if err != nil {
		t.Fatal(err)
	}
	backup := []byte(nupMetaBackupMagic)
	backup = binary.LittleEndian.AppendUint64(backup, uint64(len(original)))
	backup = append(backup, original[0x18:0x38]...)
	backup = binary.LittleEndian.AppendUint32(backup, 2)
	for _, pkg := range container.Packages[:2] {
		backup = binary.LittleEndian.AppendUint64(backup, pkg.metaOffset)
		backup = binary.LittleEndian.AppendUint32(backup, uint32(pkg.metaSize))
		backup = append(backup, original[pkg.metaOffset:pkg.metaOffset+pkg.metaSize]...)
	}
	half := bytes.Clone(fixed)
	second := container.Packages[1]
	copy(half[second.metaOffset:], original[second.metaOffset:second.metaOffset+second.metaSize])
	os.WriteFile(path, half, 0o644)
	os.WriteFile(NupMetaBackup(path), backup, 0o644)
	if n, err := FixNupZeroBased(path); err != nil || n != 2 {
		t.Fatalf("after a stop: %d %v", n, err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, fixed) {
		t.Error("not the same as fixed at once")
	}

	// a copy cut short is not put back, not even its first block
	os.WriteFile(path, half, 0o644)
	os.WriteFile(NupMetaBackup(path), backup[:len(backup)-10], 0o644)
	if _, err := FixNupZeroBased(path); !errors.Is(err, ErrNupMetaOtherCopy) {
		t.Errorf("cut short: %v", err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, half) {
		t.Error("written with a copy cut short")
	}

	// the copy of another NUP
	os.WriteFile(path, original, 0o644)
	os.WriteFile(NupMetaBackup(path), append([]byte(nupMetaBackupMagic), make([]byte, 64)...), 0o644)
	if _, err := FixNupZeroBased(path); !errors.Is(err, ErrNupMetaOtherCopy) || err.Error() != NupMetaBackup(path)+" is a copy of the metadata of another NUP: remove it to go on" {
		t.Errorf("another NUP: %v", err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Error("written with the copy of another NUP")
	}
	if _, err := os.Stat(NupMetaBackup(path)); err != nil {
		t.Errorf("the copy of another NUP removed: %v", err)
	}
}

// A NUP read again otherwise than before is not taken as fixed: it is put back.
func TestSameNupMeta(t *testing.T) {
	before := &NupContainer{Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{nupTlvTitleName: []byte("Quest")}, ZeroBased: true}}}
	same := &NupContainer{Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{nupTlvTitleName: []byte("Quest")}}}}
	if err := sameNupMeta(before, same); err != nil {
		t.Error(err)
	}
	for name, after := range map[string]*NupContainer{
		"still from 0":   {Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{nupTlvTitleName: []byte("Quest")}, ZeroBased: true}}},
		"other name":     {Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{nupTlvTitleName: []byte("Other")}}}},
		"other type":     {Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{nupTlvPublisher: []byte("Quest")}}}},
		"other package":  {Packages: []NupPackage{{TitleId: 2, Meta: map[uint16][]byte{nupTlvTitleName: []byte("Quest")}}}},
		"one more":       {Packages: []NupPackage{same.Packages[0], same.Packages[0]}},
		"record dropped": {Packages: []NupPackage{{TitleId: 1, Meta: map[uint16][]byte{}}}},
	} {
		if sameNupMeta(before, after) == nil {
			t.Errorf("%s: taken as the same", name)
		}
	}
}

// The blocks NXPress keeps beside a NUP are the ones the library keeps: each puts back what the
// other left.
func TestNupMetaBackupIsTheOneOfNXPress(t *testing.T) {
	if NupMetaBackup("Game.nup") != "Game.nup.nxpress-meta" || nupMetaBackupMagic != "NXPMETA1" {
		t.Error("not the copy of NXPress")
	}
}

// A NUP the library cannot write is left as it was, and no copy beside it.
func TestFixNupZeroBasedReadOnly(t *testing.T) {
	path := writeNup(t, testnup.Build(nupZeroBased()))
	original, _ := os.ReadFile(path)
	if err := os.Chmod(path, 0o444); err != nil {
		t.Skip(err)
	}
	defer os.Chmod(path, 0o644)
	if _, err := FixNupZeroBased(path); err == nil {
		t.Skip("the file can be written anyway (running as root)")
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Error("written")
	}
	if _, err := os.Stat(NupMetaBackup(path)); !os.IsNotExist(err) {
		t.Errorf("backup left: %v", err)
	}
}
