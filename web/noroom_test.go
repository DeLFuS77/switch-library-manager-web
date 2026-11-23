package web

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
)

func TestDiskFullMidWayIsNoRoom(t *testing.T) {
	var full error = syscall.ENOSPC
	if runtime.GOOS == "windows" {
		full = syscall.Errno(112) // ERROR_DISK_FULL
	}
	err := fmt.Errorf("writing the copy: %w", &os.PathError{Op: "write", Path: "game.nsz.tmp", Err: full})
	if !noRoom(err) {
		t.Fatal("a disk that filled up on the way is not told as no room")
	}
	if note := compressNote(tasklog.TASK_OPTIMIZE, err); note != NOTE_COMPRESS_SPACE {
		t.Fatalf("Optimize on a full disk says %q", note)
	}
	if note := compressNote(tasklog.TASK_COMPRESS, err); note != NOTE_COMPRESS_SPACE {
		t.Fatalf("Compress on a full disk says %q", note)
	}
}
