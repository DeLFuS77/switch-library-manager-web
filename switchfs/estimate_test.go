package switchfs

import (
	"context"
	"path/filepath"
	"testing"
)

func TestEstimateCompression(t *testing.T) {
	initNszKeys(t, "")
	folder := t.TempDir()
	source, original := writeTestNsp(t, folder)

	// small enough to be compressed whole: the estimate is close to the real size
	whole, err := EstimateCompression(context.Background(), source, CompressOptions{}, 4, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	result, err := CompressGame(context.Background(), source, filepath.Join(folder, "real.nsz"), CompressOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.InputSize != int64(len(original)) || whole.Sampled != whole.Compressible || whole.Duration <= 0 {
		t.Fatalf("estimate: %+v", whole)
	}
	if difference := whole.OutputSize - result.OutputSize; difference < -0x4000 || difference > 0x4000 {
		t.Fatalf("estimated %d bytes, compressed to %d", whole.OutputSize, result.OutputSize)
	}

	// with samples smaller than the game: only part of it is compressed
	sampled, err := EstimateCompression(context.Background(), source, CompressOptions{Level: "3"}, 2, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	if sampled.Sampled >= sampled.Compressible || sampled.OutputSize <= 0 || sampled.OutputSize >= sampled.InputSize {
		t.Fatalf("sampled estimate: %+v", sampled)
	}

	// an NSZ holds nothing more to compress
	if _, err := EstimateCompression(context.Background(), filepath.Join(folder, "real.nsz"), CompressOptions{}, 2, 1<<20); err == nil {
		t.Fatal("an NSZ has nothing to estimate")
	}
}
