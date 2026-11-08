package web

import (
	"crypto/rand"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// The speed test of the network install: a file made in memory that an installer downloads to
// measure the network alone, without the disk of the server or the library. Behind the same
// gate and keys as the rest of /install/.

const (
	netInstallSpeedTest = NET_INSTALL_PREFIX + "speedtest"
	// the size given when none is asked, and the largest one
	netSpeedDefault = 1 << 30
	netSpeedMax     = 8 << 30
	// the block repeated to make the file
	netSpeedBlock = 1 << 20
)

var (
	netSpeedOnce sync.Once
	netSpeedData []byte
)

// netSpeedBlockData is a block of random bytes, made once: nothing on the way can compress it.
func netSpeedBlockData() []byte {
	netSpeedOnce.Do(func() {
		netSpeedData = make([]byte, netSpeedBlock)
		rand.Read(netSpeedData)
	})
	return netSpeedData
}

// netSpeedReader is a file of a given size made of the block repeated, with Seek for ranges.
type netSpeedReader struct {
	size, offset int64
	block        []byte
}

func (s *netSpeedReader) Read(p []byte) (int, error) {
	if s.offset >= s.size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && s.offset < s.size {
		at := s.offset % int64(len(s.block))
		chunk := copy(p[n:], s.block[at:])
		if left := s.size - s.offset; int64(chunk) > left {
			chunk = int(left)
		}
		n += chunk
		s.offset += int64(chunk)
	}
	return n, nil
}

func (s *netSpeedReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += s.offset
	case io.SeekEnd:
		offset += s.size
	}
	if offset < 0 {
		return 0, io.ErrUnexpectedEOF
	}
	s.offset = offset
	return offset, nil
}

// handleNetSpeedTest gives the file of the speed test; ?size= asks for its size in bytes.
func (web *Web) handleNetSpeedTest(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.netInstallGate(w, r); !ok {
		return
	}
	size := int64(netSpeedDefault)
	if asked, err := strconv.ParseInt(r.URL.Query().Get("size"), 10, 64); err == nil && asked > 0 {
		size = min(asked, netSpeedMax)
	}
	http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", time.Time{}, &netSpeedReader{size: size, block: netSpeedBlockData()})
}
