package switchfs

import (
	"errors"
	"io"
	"sync"
)

// readAhead reads a reader on a goroutine of its own, in chunks of a size, up to a number of
// chunks ahead of what is read from it: a decompression goes on while the data before it is
// decrypted, hashed and written. Close stops the goroutine; it is called before the reader
// read is closed.
type readAhead struct {
	chunks  chan []byte
	free    chan []byte
	stop    chan struct{}
	stopped sync.WaitGroup
	// the chunk being read, and what is left of it
	chunk   []byte
	current []byte
	// what ended the reading, told once the chunks before it are read
	err error
}

func newReadAhead(source io.Reader, size int, count int) *readAhead {
	r := &readAhead{chunks: make(chan []byte, count), free: make(chan []byte, count+1), stop: make(chan struct{})}
	for i := 0; i < count+1; i++ {
		r.free <- make([]byte, size)
	}
	r.stopped.Add(1)
	go func() {
		defer r.stopped.Done()
		defer close(r.chunks)
		for {
			var buffer []byte
			select {
			case buffer = <-r.free:
			case <-r.stop:
				return
			}
			n, err := io.ReadFull(source, buffer)
			if n > 0 {
				select {
				case r.chunks <- buffer[:n]:
				case <-r.stop:
					return
				}
			}
			if err != nil {
				// the data ends in the middle of a chunk: the end, as for any reader
				if errors.Is(err, io.ErrUnexpectedEOF) {
					err = io.EOF
				}
				r.err = err
				return
			}
		}
	}()
	return r
}

func (r *readAhead) Read(p []byte) (int, error) {
	for len(r.current) == 0 {
		if r.chunk != nil {
			r.free <- r.chunk[:cap(r.chunk)]
			r.chunk = nil
		}
		chunk, ok := <-r.chunks
		if !ok {
			// the goroutine has ended: its error is set
			return 0, r.err
		}
		r.chunk, r.current = chunk, chunk
	}
	n := copy(p, r.current)
	r.current = r.current[n:]
	return n, nil
}

// Close stops the reading ahead and waits for it to end.
func (r *readAhead) Close() {
	select {
	case <-r.stop:
	default:
		close(r.stop)
	}
	r.stopped.Wait()
}
