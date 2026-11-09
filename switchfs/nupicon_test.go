package switchfs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/testnup"
)

// sidedImage is an image wide or tall, red at its ends and of a color in the middle square.
func sidedImage(width, height int, middle color.Color) image.Image {
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	side := min(width, height)
	for y := range height {
		for x := range width {
			if x >= (width-side)/2 && x < (width-side)/2+side && y >= (height-side)/2 && y < (height-side)/2+side {
				picture.Set(x, y, middle)
			} else {
				picture.Set(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}
	return picture
}

func pngBytes(t *testing.T, picture image.Image) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMakeNupIconCutsTheMiddleSquare(t *testing.T) {
	green := color.RGBA{0, 200, 0, 255}
	var jpg bytes.Buffer
	jpeg.Encode(&jpg, sidedImage(120, 500, green), &jpeg.Options{Quality: 95})
	for name, data := range map[string][]byte{"wide png": pngBytes(t, sidedImage(900, 300, green)), "tall jpeg": jpg.Bytes(), "small": pngBytes(t, sidedImage(40, 64, green))} {
		icon, err := MakeNupIcon(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		picture, err := jpeg.Decode(bytes.NewReader(icon))
		if err != nil || picture.Bounds().Dx() != NupIconSize || picture.Bounds().Dy() != NupIconSize {
			t.Fatalf("%s: %v %v", name, err, picture.Bounds())
		}
		for _, point := range []image.Point{{1, 1}, {254, 1}, {1, 254}, {254, 254}, {128, 128}} {
			r, g, b, _ := picture.At(point.X, point.Y).RGBA()
			if r>>8 > 50 || g>>8 < 160 || b>>8 > 50 {
				t.Errorf("%s at %v: %d %d %d", name, point, r>>8, g>>8, b>>8)
			}
		}
	}
	if _, err := MakeNupIcon([]byte("not an image")); err == nil {
		t.Error("made an icon of text")
	}
}

// nupNoIcon is a game, its update and a DLC without an icon.
func nupNoIcon() []testnup.Package {
	packages := nupPlayable()
	packages[0].Meta = []testnup.Record{tlv(1, []byte("NPDM made up")), tlv(4, []byte("1.0.0")), tlv(5, []byte("Made-up Quest")),
		tlv(6, []byte("Nobody Games")), tlv(8, []byte("Made-up Dump"))}
	return packages
}

func TestSetNupIcon(t *testing.T) {
	icon, err := MakeNupIcon(pngBytes(t, sidedImage(300, 300, color.White)))
	if err != nil {
		t.Fatal(err)
	}
	path := writeNup(t, testnup.Build(nupNoIcon()))
	before, err := ReadNup(path)
	if err != nil || before.HasIcon() {
		t.Fatalf("%v", err)
	}
	beforeMeta, _ := ReadNupMetadata(path)
	if set, err := SetNupIcon(path, icon); err != nil || !set {
		t.Fatalf("%v %v", set, err)
	}
	after, err := ReadNup(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := after.Icon(); err != nil || !bytes.Equal(got, icon) {
		t.Fatalf("icon: %v", err)
	}
	afterMeta, _ := ReadNupMetadata(path)
	for id, meta := range beforeMeta {
		if afterMeta[id] == nil || afterMeta[id].Version != meta.Version {
			t.Errorf("%s: %+v", id, afterMeta[id])
		}
	}
	if string(after.Packages[0].Meta[nupTlvTitleName]) != "Made-up Quest" {
		t.Errorf("%+v", after.Packages[0].Meta)
	}
	if _, err := VerifyNup(context.Background(), path, nil); err != nil {
		t.Errorf("verify: %v", err)
	}
	if emulators := after.Emulators(); !emulators.Ok() {
		t.Errorf("emulators: %+v", emulators)
	}
	if _, err := os.Stat(NupIconBackup(path)); !os.IsNotExist(err) {
		t.Errorf("backup left: %v", err)
	}
	// again: the icon it carries is kept
	written, _ := os.ReadFile(path)
	if set, err := SetNupIcon(path, testnup.Jpeg); err != nil || set {
		t.Errorf("again: %v %v", set, err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, written) {
		t.Error("changed again")
	}
}

func TestSetNupIconCountedFromZero(t *testing.T) {
	packages := nupZeroBased()
	packages[0].Meta = append(packages[0].Meta[:1], packages[0].Meta[2:]...)
	path := writeNup(t, testnup.Build(packages))
	icon, _ := MakeNupIcon(pngBytes(t, sidedImage(64, 64, color.White)))
	if set, err := SetNupIcon(path, icon); err != nil || !set {
		t.Fatalf("%v %v", set, err)
	}
	after, _ := ReadNup(path)
	if got, _ := after.Icon(); !after.Packages[0].ZeroBased || !bytes.Equal(got, icon) {
		t.Fatalf("%+v", after.Packages[0])
	}
	if fixed, err := FixNupZeroBased(path); err != nil || fixed == 0 {
		t.Fatalf("%d %v", fixed, err)
	}
	if fixed, _ := ReadNup(path); !bytes.Equal(fixed.Packages[0].Meta[nupTlvIcon], icon) {
		t.Error("the icon is not where the format puts it")
	}
}

func TestSetNupIconAfterAStop(t *testing.T) {
	icon, _ := MakeNupIcon(pngBytes(t, sidedImage(64, 64, color.White)))
	path := writeNup(t, testnup.Build(nupNoIcon()))
	original, _ := os.ReadFile(path)
	if _, err := SetNupIcon(path, icon); err != nil {
		t.Fatal(err)
	}
	given, _ := os.ReadFile(path)
	backup := func(original []byte, given []byte) []byte {
		le := func(v uint64) []byte {
			b := make([]byte, 8)
			for i := range b {
				b[i] = byte(v >> (8 * i))
			}
			return b
		}
		read := func(at int) uint64 {
			v := uint64(0)
			for i := 7; i >= 0; i-- {
				v = v<<8 | uint64(original[at+i])
			}
			return v
		}
		tocOffset, tocSize := read(0x08), read(0x10)
		data := []byte(nupIconBackupMagic)
		data = append(data, le(uint64(len(original)))...)
		data = append(data, le(uint64(len(given)))...)
		data = append(data, given[0x18:0x38]...)
		data = append(data, original[:nupHeaderSize]...)
		data = append(data, le(tocOffset)...)
		return append(data, original[tocOffset:tocOffset+tocSize]...)
	}
	// stopped once all was written: put back as it was, then given the icon again
	os.WriteFile(NupIconBackup(path), backup(original, given), 0o644)
	if set, err := SetNupIcon(path, icon); err != nil || !set {
		t.Fatalf("%v %v", set, err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, given) {
		t.Error("not the same after it was put back")
	}
	// stopped before the header was written: the NUP is longer than it says
	os.WriteFile(path, append(bytes.Clone(original), given[len(original):]...), 0o644)
	os.WriteFile(NupIconBackup(path), backup(original, given), 0o644)
	if err := restoreNupIcon(path); err != nil {
		t.Fatal(err)
	}
	if now, _ := os.ReadFile(path); !bytes.Equal(now, original) {
		t.Error("not put back")
	}
	// a copy of another NUP is left
	other := testnup.Build(nupGame())
	os.WriteFile(NupIconBackup(path), backup(other, other), 0o644)
	if _, err := SetNupIcon(path, icon); err == nil || !strings.Contains(err.Error(), "another NUP") {
		t.Errorf("%v", err)
	}
}
