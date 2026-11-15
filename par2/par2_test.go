package par2

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func writeRandom(t *testing.T, path string, size int, seed int64) []byte {
	t.Helper()
	data := make([]byte, size)
	rand.New(rand.NewSource(seed)).Read(data)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

// The field: every element but 0 has its inverse, and a matrix of the constants of PAR 2.0
// times its inverse is the identity.
func TestField(t *testing.T) {
	for a := 1; a < 65536; a += 97 {
		if gfMul(uint16(a), gfInv(uint16(a))) != 1 {
			t.Fatalf("inverse of %d", a)
		}
	}
	logs := inputLogs(5)
	if logs[0] != 1 || logs[1] != 2 || logs[2] != 4 || logs[3] != 7 || logs[4] != 8 {
		t.Fatalf("logs %v", logs)
	}
	m := [][]uint16{}
	for e := 0; e < 4; e++ {
		row := []uint16{}
		for _, l := range inputLogs(4) {
			row = append(row, coefficient(l, e))
		}
		m = append(m, row)
	}
	inv, ok := invert(m)
	if !ok {
		t.Fatal("no inverse")
	}
	for i := range m {
		for j := range m {
			var sum uint16
			for k := range m {
				sum ^= gfMul(m[i][k], inv[k][j])
			}
			if (i == j) != (sum == 1) || (i != j && sum != 0) {
				t.Fatalf("m×inv at %d,%d = %d", i, j, sum)
			}
		}
	}
	// mulAdd is the product word by word
	src := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	dst := make([]byte, len(src))
	newMulTable(0x1234).mulAdd(dst, src)
	for i := 0; i < len(src); i += 2 {
		want := gfMul(0x1234, uint16(src[i])|uint16(src[i+1])<<8)
		if uint16(dst[i])|uint16(dst[i+1])<<8 != want {
			t.Fatalf("word %d", i/2)
		}
	}
}

// A file damaged in several places, cut short or made longer is rebuilt as it was, as long as
// it has no more damaged slices than recovery slices; with more, it is left as it is.
func TestCreateVerifyRepair(t *testing.T) {
	for _, budget := range []int64{256 << 20, 3 * 4096} {
		MemoryBudget = budget
		folder := t.TempDir()
		file := filepath.Join(folder, "game.nsz")
		original := writeRandom(t, file, 3*1024*1024+123, 1)
		recovery := Path(file)
		if err := Create(context.Background(), file, recovery, 5, nil); err != nil {
			t.Fatal(err)
		}
		result, err := Verify(context.Background(), recovery, file, nil)
		if err != nil || !result.OK() || result.Recovery < 2 {
			t.Fatalf("verify: %+v %v", result, err)
		}

		damage := func(change func([]byte) []byte) {
			data := change(append([]byte(nil), original...))
			if err := os.WriteFile(file, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		check := func(name string) {
			t.Helper()
			result, err := Repair(context.Background(), recovery, file, nil)
			if err != nil || result.OK() {
				t.Fatalf("%s: repair %+v %v", name, result, err)
			}
			got, _ := os.ReadFile(file)
			if !bytes.Equal(got, original) {
				t.Fatalf("%s: not rebuilt", name)
			}
			if again, err := Verify(context.Background(), recovery, file, nil); err != nil || !again.OK() {
				t.Fatalf("%s: after %+v %v", name, again, err)
			}
		}
		damage(func(d []byte) []byte { d[10] ^= 1; d[len(d)/2] ^= 0xFF; d[len(d)-1] ^= 4; return d })
		check("bits")
		damage(func(d []byte) []byte { return d[:len(d)-5000] })
		check("cut short")
		damage(func(d []byte) []byte { return append(d, 1, 2, 3) })
		check("longer")

		// more damaged slices than recovery slices: left as it is
		damage(func(d []byte) []byte {
			for i := 0; i < len(d); i += 4096 {
				d[i] ^= 1
			}
			return d
		})
		before, _ := os.ReadFile(file)
		if _, err := Repair(context.Background(), recovery, file, nil); !errors.Is(err, ErrTooDamaged) {
			t.Fatalf("too damaged: %v", err)
		}
		if after, _ := os.ReadFile(file); !bytes.Equal(after, before) {
			t.Fatal("a file too damaged is left as it is")
		}
	}
	MemoryBudget = 256 << 20
}

// A recovery file damaged in places still checks and rebuilds with the packets it has left.
func TestDamagedRecoveryFile(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "game.xcz")
	original := writeRandom(t, file, 1024*1024, 2)
	recovery := Path(file)
	if err := Create(context.Background(), file, recovery, 10, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(recovery)
	// the first recovery slice and the first copy of the packets that check the file
	data[200] ^= 1
	data[len(data)-2*len(data)/100-3000] ^= 1
	os.WriteFile(recovery, data, 0o644)
	broken := append([]byte(nil), original...)
	broken[5] ^= 1
	os.WriteFile(file, broken, 0o644)
	if _, err := Repair(context.Background(), recovery, file, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, original) {
		t.Fatal("not rebuilt")
	}
}

func BenchmarkCreate(b *testing.B) {
	folder := b.TempDir()
	file := filepath.Join(folder, "big.nsz")
	data := make([]byte, 256<<20)
	rand.New(rand.NewSource(3)).Read(data)
	os.WriteFile(file, data, 0o644)
	b.SetBytes(int64(len(data)))
	for i := 0; i < b.N; i++ {
		if err := Create(context.Background(), file, Path(file), 3, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDescribe(t *testing.T) {
	folder := t.TempDir()
	file := filepath.Join(folder, "game.nsp")
	writeRandom(t, file, 2*1024*1024, 4)
	if err := Create(context.Background(), file, Path(file), 10, nil); err != nil {
		t.Fatal(err)
	}
	slices, recovery, err := Describe(Path(file))
	if err != nil || slices != 512 || recovery != 52 {
		t.Fatalf("%d slices, %d recovery: %v", slices, recovery, err)
	}
	if _, _, err := Describe(file); err == nil {
		t.Fatal("a game is no recovery file")
	}
}
