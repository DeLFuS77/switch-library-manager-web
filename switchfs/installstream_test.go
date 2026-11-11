package switchfs

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readStream reads a whole install stream through its reader.
func readStream(t *testing.T, stream *InstallStream) []byte {
	t.Helper()
	t.Cleanup(CloseIdleInstallDecoders)
	reader := stream.Reader()
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != stream.Size() {
		t.Fatalf("read %d bytes, the stream tells %d", len(data), stream.Size())
	}
	return data
}

// filesOf gives the files of a PFS0 by name.
func filesOf(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	pfs0, err := readPfs0(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, file := range pfs0.Files {
		start := int64(file.StartOffset)
		files[file.Name] = data[start : start+int64(file.Size)]
	}
	return files
}

// An NSZ with an NCA in sparse storage (NCZSECT2) and a plain one gives an NSZ with the first
// decompressed, byte for byte the NCA of the original, and the second as it was.
func TestInstallStreamGivesNczSect2Decompressed(t *testing.T) {
	initNszKeys(t, "")
	sparse, plain := sparseNca(t), makeProgramNca(t, gameData(0x60000), 3, "")
	source := filepath.Join(t.TempDir(), "Game [0100000000010000][v0].nsp")
	names := []string{contentName(sparse, ".nca"), contentName(plain, ".nca")}
	os.WriteFile(source, makePFS0(pfs0Magic, names, [][]byte{sparse, plain}), 0o644)
	target := NszPath(source)
	if _, err := CompressGame(context.Background(), source, target, CompressOptions{Sparse: true}); err != nil {
		t.Fatal(err)
	}
	stream, err := OpenInstallStream(target)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Translated != 1 || stream.Kept != 1 || stream.Extension() != ".nsz" {
		t.Fatalf("translated %d kept %d %s", stream.Translated, stream.Kept, stream.Extension())
	}
	files := filesOf(t, readStream(t, stream))
	if !bytes.Equal(files[names[0]], sparse) {
		t.Fatal("the NCA in sparse storage differs")
	}
	packed, _ := os.ReadFile(target)
	kept := strings.TrimSuffix(names[1], ".nca") + ".ncz"
	compressed := filesOf(t, packed)[kept]
	if compressed == nil || !bytes.Equal(files[kept], compressed) {
		t.Fatal("the plain NCZ was not given as it is")
	}
}

// An NCZSECT3 gives its NCA, the LZ4 blocks made again; the whole package is an NSP.
func TestInstallStreamGivesNczSect3Decompressed(t *testing.T) {
	initNszKeys(t, "")
	nca := compressedNca(t, 5, 5, 0)
	source := writeOneNcaNsp(t, nca)
	compressBack(t, source)
	stream, err := OpenInstallStream(NszPath(source))
	if err != nil {
		t.Fatal(err)
	}
	if stream.Translated != 1 || stream.Kept != 0 || stream.Extension() != ".nsp" {
		t.Fatalf("translated %d kept %d %s", stream.Translated, stream.Kept, stream.Extension())
	}
	original, _ := os.ReadFile(source)
	if !bytes.Equal(filesOf(t, readStream(t, stream))[contentName(nca, ".nca")], nca) || len(original) == 0 {
		t.Fatal("the NCA differs")
	}
}

// Pieces asked for one after the other, as an installer does with range requests, and in any
// order, give what the whole package has there; the pieces in order share one decoder.
func TestInstallStreamPieces(t *testing.T) {
	initNszKeys(t, "")
	source := writeOneNcaNsp(t, sparseNca(t))
	if _, err := CompressGame(context.Background(), source, NszPath(source), CompressOptions{Sparse: true}); err != nil {
		t.Fatal(err)
	}
	stream, err := OpenInstallStream(NszPath(source))
	if err != nil {
		t.Fatal(err)
	}
	whole := readStream(t, stream)
	piece := func(start, length int64) []byte {
		reader := stream.Reader()
		defer reader.Close()
		reader.Seek(start, io.SeekStart)
		data := make([]byte, length)
		if _, err := io.ReadFull(reader, data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	const step = 0x3000
	for start := int64(0); start < stream.Size(); start += step {
		length := min(step, stream.Size()-start)
		if !bytes.Equal(piece(start, length), whole[start:start+length]) {
			t.Fatalf("piece at %#x differs", start)
		}
		if IdleInstallDecoders() > 1 {
			t.Fatalf("%d decoders for pieces in order", IdleInstallDecoders())
		}
	}
	random := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		start := random.Int63n(stream.Size())
		length := min(random.Int63n(0x8000)+1, stream.Size()-start)
		if !bytes.Equal(piece(start, length), whole[start:start+length]) {
			t.Fatalf("piece at %#x of %#x differs", start, length)
		}
	}
}

// A file without an experimental NCZ has nothing to translate.
func TestInstallStreamNothingToTranslate(t *testing.T) {
	initNszKeys(t, "")
	source := writeOneNcaNsp(t, makeProgramNca(t, gameData(0x60000), 3, ""))
	for _, path := range []string{source, NszPath(source)} {
		if path != source {
			if _, err := CompressGame(context.Background(), source, path, CompressOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		stream, err := OpenInstallStream(path)
		if err != nil || stream.Translated != 0 {
			t.Fatalf("%s: %+v %v", filepath.Base(path), stream, err)
		}
	}
}
