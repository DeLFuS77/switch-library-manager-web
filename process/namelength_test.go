package process

import (
	"strings"
	"testing"
)

// A name of kana with marks takes more bytes on a disk that keeps it decomposed, and is
// shortened before its title ID, which stays with the version and the extension.
func TestFitFileName(t *testing.T) {
	name := "SDガンダム ジージェネレーション クロスレイズ プレミアムGサウンドエディション [追加派遣作戦セット④（プレミアムGサウンドエディション用）] [010022900D3ED01C][JP][v65536].nsp"
	if len(name) > 240 || NameBytes(name) <= 255 {
		t.Fatalf("the name: %d bytes, %d decomposed", len(name), NameBytes(name))
	}
	fitted := FitFileName(name, 240)
	if NameBytes(fitted) > 240 || !strings.HasSuffix(fitted, "] [010022900D3ED01C][JP][v65536].nsp") || !strings.HasPrefix(fitted, "SDガンダム") {
		t.Fatalf("%q (%d bytes)", fitted, NameBytes(fitted))
	}
	if strings.Count(fitted, "[") != strings.Count(fitted, "]") {
		t.Fatalf("a bracket left open: %q", fitted)
	}
	if short := "Game [0100000000010000][v0].nsp"; FitFileName(short, 240) != short {
		t.Fatal("a short name is kept")
	}
	long := strings.Repeat("é", 200) + ".xci"
	if fitted := FitFileName(long, 240); NameBytes(fitted) > 240 || !strings.HasSuffix(fitted, ".xci") {
		t.Fatalf("a name without a title ID: %q", fitted)
	}
}
