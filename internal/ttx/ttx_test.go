package ttx_test

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/ttx"
	"github.com/dtrunk90/switch-library-manager-web/internal/ttx/ttxfake"
)

func receiver(t *testing.T) (*ttxfake.Receiver, *ttx.Client) {
	t.Helper()
	r, err := ttxfake.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	client, err := ttx.Dial(context.Background(), r.Address(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return r, client
}

func randomBytes(size int) []byte {
	data := make([]byte, size)
	rand.New(rand.NewSource(int64(size))).Read(data)
	return data
}

// A file goes whole, byte for byte, into folders made on the way; the sums of PUT and HASH are
// the ones of the file, LIST shows it, GET reads it back and DELETE removes it.
func TestPutHashListGetDelete(t *testing.T) {
	r, client := receiver(t)
	ctx := context.Background()
	info, err := client.Ping(ctx)
	if err != nil || info.Version != "nx2-flow test switch2" || info.Free != 64<<30 || info.Total != 128<<30 {
		t.Fatalf("ping: %+v %v", info, err)
	}
	data := randomBytes(5<<20 + 123)
	var steps []int64
	sum, err := client.Put(ctx, "/nx2-flow/games/A Game.nup", bytes.NewReader(data), int64(len(data)), func(sent int64) { steps = append(steps, sent) })
	if err != nil || sum != crc32.ChecksumIEEE(data) {
		t.Fatalf("put: %08x %v", sum, err)
	}
	if len(steps) < 5 || steps[len(steps)-1] != int64(len(data)) {
		t.Fatalf("progress: %v", steps)
	}
	written, err := os.ReadFile(filepath.Join(r.Root, "nx2-flow", "games", "A Game.nup"))
	if err != nil || !bytes.Equal(written, data) {
		t.Fatalf("the file on the card: %d bytes, %v", len(written), err)
	}
	size, hash, err := client.Hash(ctx, "/nx2-flow/games/A Game.nup")
	if err != nil || size != uint64(len(data)) || hash != crc32.ChecksumIEEE(data) {
		t.Fatalf("hash: %d %08x %v", size, hash, err)
	}
	if err := client.Mkdir(ctx, "/nx2-flow/covers/full"); err != nil {
		t.Fatal(err)
	}
	entries, err := client.List(ctx, "/nx2-flow")
	if err != nil || len(entries) != 2 {
		t.Fatalf("list: %+v %v", entries, err)
	}
	games, err := client.List(ctx, "/nx2-flow/games")
	if err != nil || len(games) != 1 || games[0].Name != "A Game.nup" || games[0].Dir || games[0].Size != uint64(len(data)) {
		t.Fatalf("the games: %+v %v", games, err)
	}
	var back bytes.Buffer
	if got, err := client.Get(ctx, "/nx2-flow/games/A Game.nup", &back, -1); err != nil || got != int64(len(data)) || !bytes.Equal(back.Bytes(), data) {
		t.Fatalf("get: %d %v", got, err)
	}
	if err := client.Delete(ctx, "/nx2-flow/games/A Game.nup"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "nx2-flow", "games", "A Game.nup")); !os.IsNotExist(err) {
		t.Fatalf("deleted: %v", err)
	}
	// a refusal leaves the connection usable
	var remote *ttx.RemoteError
	if _, _, err := client.Hash(ctx, "/nx2-flow/games/A Game.nup"); !errors.As(err, &remote) || !strings.Contains(remote.Message, "stat") {
		t.Fatalf("hash of nothing: %v", err)
	}
	if _, err := client.Ping(ctx); err != nil {
		t.Fatalf("after a refusal: %v", err)
	}
	// an empty file too
	if _, err := client.Put(ctx, "/empty.bin", bytes.NewReader(nil), 0, nil); err != nil {
		t.Fatal(err)
	}
}

// A PUT replaces a file of the same name, and nothing of the part file stays.
func TestPutReplaces(t *testing.T) {
	r, client := receiver(t)
	ctx := context.Background()
	for _, text := range []string{"the first one, longer", "second"} {
		if _, err := client.Put(ctx, "/a/b.nup", strings.NewReader(text), int64(len(text)), nil); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(filepath.Join(r.Root, "a", "b.nup"))
	if string(data) != "second" {
		t.Fatalf("replaced: %q", data)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "a", "b.nup"+ttx.PartSuffix)); !os.IsNotExist(err) {
		t.Fatal("the part file stays")
	}
}

// Cancelling a PUT halfway cuts the connection: the receiver keeps neither the file nor its part.
func TestPutCancelledLeavesNothing(t *testing.T) {
	r, client := receiver(t)
	ctx, cancel := context.WithCancel(context.Background())
	r.Set(func(r *ttxfake.Receiver) {
		r.OnData = func(path string, written int64) {
			if written >= 2<<20 {
				cancel()
			}
		}
	})
	data := randomBytes(16 << 20)
	_, err := client.Put(ctx, "/nx2-flow/games/Cut.nup", bytes.NewReader(data), int64(len(data)), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
	if !r.Wait(5 * time.Second) {
		t.Fatal("the receiver still holds the connection")
	}
	for _, name := range []string{"Cut.nup", "Cut.nup" + ttx.PartSuffix} {
		if _, err := os.Stat(filepath.Join(r.Root, "nx2-flow", "games", name)); !os.IsNotExist(err) {
			t.Fatalf("%s stays: %v", name, err)
		}
	}
	if _, err := client.Ping(context.Background()); !errors.Is(err, ttx.ErrClosed) {
		t.Fatalf("the connection is closed: %v", err)
	}
}

// A source shorter than its size is not sent as a whole file.
func TestPutShortSource(t *testing.T) {
	r, client := receiver(t)
	_, err := client.Put(context.Background(), "/short.nup", strings.NewReader("abc"), 10, nil)
	if !errors.Is(err, ttx.ErrShortSource) {
		t.Fatalf("short: %v", err)
	}
	r.Wait(5 * time.Second)
	if _, err := os.Stat(filepath.Join(r.Root, "short.nup")); !os.IsNotExist(err) {
		t.Fatal("a short file arrived")
	}
}

// A sum that is not the one sent is an error.
func TestPutWrongSum(t *testing.T) {
	r, client := receiver(t)
	r.Set(func(r *ttxfake.Receiver) { r.WrongPutSum = true })
	if _, err := client.Put(context.Background(), "/x.bin", strings.NewReader("data"), 4, nil); !errors.Is(err, ttx.ErrMismatch) {
		t.Fatalf("wrong sum: %v", err)
	}
}

// Paths the receiver refuses are refused before anything is sent; the receiver hangs up on a bad
// path all the same.
func TestBadPaths(t *testing.T) {
	_, client := receiver(t)
	for _, path := range []string{"", "games/a.nup", "/a/../b", `/a\b`, "/sdmc:/a", "/" + strings.Repeat("a", ttx.MaxPath)} {
		if err := client.Mkdir(context.Background(), path); !errors.Is(err, ttx.ErrBadPath) {
			t.Errorf("%q: %v", path, err)
		}
	}
	if _, err := client.Ping(context.Background()); err != nil {
		t.Fatalf("nothing was sent: %v", err)
	}
}

// Nothing listening: Dial fails at once.
func TestDialRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	if _, err := ttx.Dial(context.Background(), address, time.Second); err == nil {
		t.Fatal("dialled nothing")
	}
}

// The receiver hangs up on a request that is not one.
func TestReceiverHangsUpOnGarbage(t *testing.T) {
	r, _ := receiver(t)
	conn, err := net.Dial("tcp", r.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("HELLO, WORLD"))
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if n, err := conn.Read(make([]byte, 8)); n != 0 || err == nil {
		t.Fatalf("answered: %d %v", n, err)
	}
}
