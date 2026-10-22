package switchfs

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTrimXci(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source := writeTestXci(t, folder)
	data, _ := os.ReadFile(source)
	input, _ := os.Open(source)
	end, err := XciDataEnd(input)
	input.Close()
	// the test XCI ends where its last partition ends
	if err != nil || end != (int64(len(data))+0x1FF)&^0x1FF {
		t.Fatalf("data end %d (%v) of %d bytes", end, err, len(data))
	}
	// as dumped from a game card: padded with 0xFF
	padded := append(append(append([]byte(nil), data...), bytes.Repeat([]byte{0}, int(end)-len(data))...), bytes.Repeat([]byte{0xFF}, 0x30000)...)
	card := filepath.Join(folder, "card.xci")
	os.WriteFile(card, padded, 0o644)

	// to another file
	trimmed := filepath.Join(folder, "trimmed.xci")
	var last, total int64
	removed, err := TrimXci(context.Background(), card, trimmed, func(done, all int64) { last, total = done, all })
	if err != nil || removed != 0x30000 || last != total {
		t.Fatalf("trim: %d %v (%d of %d)", removed, err, last, total)
	}
	if got, _ := os.ReadFile(trimmed); !bytes.Equal(got, padded[:end]) {
		t.Fatal("the trimmed XCI is the data of the original")
	}
	if _, err := VerifyFile(context.Background(), trimmed, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := TrimXci(context.Background(), trimmed, filepath.Join(folder, "again.xci"), nil); !errors.Is(err, ErrNothingToTrim) {
		t.Fatalf("a trimmed XCI: %v", err)
	}

	// in place
	if removed, err := TrimXci(context.Background(), card, card, nil); err != nil || removed != 0x30000 {
		t.Fatalf("in place: %d %v", removed, err)
	}
	if info, _ := os.Stat(card); info.Size() != end {
		t.Fatalf("in place: %d bytes, want %d", info.Size(), end)
	}

	// anything but padding after the data: left as it is
	damaged := append(append([]byte(nil), padded...), 1)
	odd := filepath.Join(folder, "odd.xci")
	os.WriteFile(odd, damaged, 0o644)
	if _, err := TrimXci(context.Background(), odd, odd, nil); err == nil {
		t.Fatal("data after the partitions is not padding")
	}
	if info, _ := os.Stat(odd); info.Size() != int64(len(damaged)) {
		t.Fatal("an XCI that cannot be trimmed is left as it is")
	}

	// an NSP is not an XCI
	nsp, _ := writeTestNsp(t, folder)
	if _, err := TrimXci(context.Background(), nsp, nsp, nil); err == nil {
		t.Fatal("only XCI files are trimmed")
	}
}
