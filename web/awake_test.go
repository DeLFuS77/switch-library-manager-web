package web

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/awake"
)

// A task on the files keeps the computer awake while it works, lets it sleep while paused, and
// once it ends.
func TestFileTaskKeepsAwake(t *testing.T) {
	web, path := compressWeb(t)
	var held atomic.Int32
	blocked := awake.BlockSleep
	awake.BlockSleep = func() func() {
		held.Add(1)
		return func() { held.Add(-1) }
	}
	t.Cleanup(func() { awake.BlockSleep = blocked })

	working, gate := make(chan struct{}), make(chan struct{})
	started := web.runFileTask(TASK_COMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		report(0)(1, 2)
		close(working)
		<-gate
		return 0, ctx.Err()
	})
	if !started {
		t.Fatal("the compression must start")
	}
	<-working
	if held.Load() != 1 {
		t.Fatalf("a task at work holds %d blocks of the sleep, want 1", held.Load())
	}
	web.pauseCompression(true)
	if held.Load() != 0 {
		t.Fatalf("a paused task holds %d blocks of the sleep, want 0", held.Load())
	}
	web.pauseCompression(false)
	if held.Load() != 1 {
		t.Fatalf("a task going on again holds %d blocks of the sleep, want 1", held.Load())
	}
	close(gate)
	waitForCompression(t, web)
	if held.Load() != 0 {
		t.Fatalf("a task that ended holds %d blocks of the sleep, want 0", held.Load())
	}

	// cancelled while paused: nothing is left held either
	working, gate = make(chan struct{}), make(chan struct{})
	web.runFileTask(TASK_COMPRESS, []string{path}, 1, func(ctx context.Context, path string, report func(int64) func(int64, int64)) (int64, error) {
		report(0)(1, 2)
		close(working)
		<-gate
		return 0, ctx.Err()
	})
	<-working
	web.pauseCompression(true)
	web.cancelCompression()
	close(gate)
	waitForCompression(t, web)
	web.pauseCompression(false)
	if held.Load() != 0 {
		t.Fatalf("a task cancelled while paused holds %d blocks of the sleep, want 0", held.Load())
	}
}
