// Package ttxfake is a receiver of the TTX1 protocol (see package ttx) that keeps the card in a
// folder of the computer, for the tests of what sends to nx2-flow. It behaves as the receiver
// documents: the folders on the way made by PUT, the file written to "<name>.ttpart" given its
// whole size and renamed over the target only when every byte arrived, the part file removed and
// the connection hung up when a PUT is cut, and the CRC-32 of zlib of what was written.
package ttxfake

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/ttx"
)

// Receiver is a fake receiver listening on the loopback address.
type Receiver struct {
	// the folder that is the card
	Root string
	// what PING tells
	Version     string
	Free, Total uint64
	// tells each block of a PUT as it is written (the path, the bytes written so far); a test
	// may cut the transfer from it
	OnData func(path string, written int64)
	// a wrong sum in the reply of a PUT, for the tests of the check
	WrongPutSum bool

	listener net.Listener
	group    sync.WaitGroup
	mutex    sync.Mutex
	conns    map[net.Conn]bool
	// the requests served, by name of the operation, and the paths of the PUT that ended
	ops  map[string]int
	puts []string
}

// Start makes a receiver of the card root and starts listening on a free port of the loopback
// address.
func Start(root string) (*Receiver, error) {
	return StartAt(root, "127.0.0.1:0")
}

// StartAt is Start on an address of choice (host:port), for a check by hand.
func StartAt(root string, address string) (*Receiver, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	r := &Receiver{Root: root, Version: "nx2-flow test switch2", Free: 64 << 30, Total: 128 << 30, listener: listener,
		conns: map[net.Conn]bool{}, ops: map[string]int{}}
	r.group.Add(1)
	go r.accept()
	return r, nil
}

// Set changes what the receiver tells or does (Version, Free, Total, OnData, WrongPutSum) while it
// may be serving.
func (r *Receiver) Set(change func(r *Receiver)) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	change(r)
}

// Address is where the receiver listens (host:port).
func (r *Receiver) Address() string {
	return r.listener.Addr().String()
}

// Close stops listening, hangs up every connection and waits for them to end.
func (r *Receiver) Close() {
	r.listener.Close()
	r.mutex.Lock()
	for conn := range r.conns {
		conn.Close()
	}
	r.mutex.Unlock()
	r.group.Wait()
}

// Wait waits, at most timeout, until no connection is open; false when one still is.
func (r *Receiver) Wait(timeout time.Duration) bool {
	end := time.Now().Add(timeout)
	for time.Now().Before(end) {
		r.mutex.Lock()
		open := len(r.conns)
		r.mutex.Unlock()
		if open == 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// Count is how many requests of an operation (PING, PUT...) were served.
func (r *Receiver) Count(op string) int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.ops[op]
}

// Puts are the paths of the PUT that ended, in order.
func (r *Receiver) Puts() []string {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return append([]string(nil), r.puts...)
}

func (r *Receiver) accept() {
	defer r.group.Done()
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		r.mutex.Lock()
		r.conns[conn] = true
		r.mutex.Unlock()
		r.group.Add(1)
		go func() {
			defer r.group.Done()
			r.serve(conn)
			conn.Close()
			r.mutex.Lock()
			delete(r.conns, conn)
			r.mutex.Unlock()
		}()
	}
}

func (r *Receiver) count(op string) {
	r.mutex.Lock()
	r.ops[op]++
	r.mutex.Unlock()
}

// onCard is the file of the folder for a path of the card; "" when the path is not allowed.
func (r *Receiver) onCard(path string) string {
	if ttx.CheckPath(path) != nil {
		return ""
	}
	path = strings.TrimRight(path, "/")
	return filepath.Join(r.Root, filepath.FromSlash(path))
}

type session struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func (s *session) ok() error {
	return s.writer.WriteByte(0)
}

func (s *session) refuse(message string) error {
	if len(message) > 0xFFFF {
		message = message[:0xFFFF]
	}
	s.writer.WriteByte(1)
	s.writer.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(message))))
	s.writer.WriteString(message)
	return s.writer.Flush()
}

func (s *session) u64(v uint64) { s.writer.Write(binary.LittleEndian.AppendUint64(nil, v)) }
func (s *session) u32(v uint32) { s.writer.Write(binary.LittleEndian.AppendUint32(nil, v)) }
func (s *session) text(v string) {
	s.writer.Write(binary.LittleEndian.AppendUint16(nil, uint16(len(v))))
	s.writer.WriteString(v)
}

// serve answers the requests of one connection until it ends or fails.
func (r *Receiver) serve(conn net.Conn) {
	s := &session{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}
	for {
		conn.SetDeadline(time.Now().Add(30 * time.Second))
		head := make([]byte, 7)
		if _, err := io.ReadFull(s.reader, head); err != nil {
			return
		}
		if string(head[:4]) != ttx.Magic {
			return
		}
		op := head[4]
		length := int(binary.LittleEndian.Uint16(head[5:7]))
		if length > ttx.MaxPath {
			return
		}
		shown := make([]byte, length)
		if _, err := io.ReadFull(s.reader, shown); err != nil {
			return
		}
		if op == ttx.OpPing {
			r.count("PING")
			r.mutex.Lock()
			free, total, version := r.Free, r.Total, r.Version
			r.mutex.Unlock()
			s.ok()
			s.u64(free)
			s.u64(total)
			s.text(version)
			if s.writer.Flush() != nil {
				return
			}
			continue
		}
		path := r.onCard(string(shown))
		if path == "" {
			// a PUT's data may follow: hung up rather than read as requests
			s.refuse("bad path")
			return
		}
		var keep bool
		switch op {
		case ttx.OpList:
			r.count("LIST")
			keep = r.list(s, path)
		case ttx.OpPut:
			r.count("PUT")
			keep = r.put(s, path, string(shown))
		case ttx.OpGet:
			r.count("GET")
			keep = r.get(s, path, true)
		case ttx.OpHash:
			r.count("HASH")
			keep = r.get(s, path, false)
		case ttx.OpDelete:
			r.count("DELETE")
			keep = r.remove(s, path, string(shown))
		case ttx.OpMkdir:
			r.count("MKDIR")
			os.MkdirAll(path, 0755)
			s.ok()
			keep = s.writer.Flush() == nil
		default:
			s.refuse("unknown command")
		}
		if !keep {
			return
		}
	}
}

func (r *Receiver) list(s *session, path string) bool {
	entries, err := os.ReadDir(path)
	if err != nil {
		return s.refuse("opendir: "+err.Error()) == nil
	}
	var body []byte
	for _, entry := range entries {
		info, err := entry.Info()
		var size, mtime uint64
		dir := byte(0)
		if err == nil {
			if info.IsDir() {
				dir = 1
			}
			size, mtime = uint64(info.Size()), uint64(info.ModTime().Unix())
		}
		body = append(body, dir)
		body = binary.LittleEndian.AppendUint64(body, size)
		body = binary.LittleEndian.AppendUint64(body, mtime)
		body = binary.LittleEndian.AppendUint16(body, uint16(len(entry.Name())))
		body = append(body, entry.Name()...)
	}
	s.ok()
	s.u32(uint32(len(entries)))
	s.u32(uint32(len(body)))
	s.writer.Write(body)
	return s.writer.Flush() == nil
}

func (r *Receiver) put(s *session, path string, shown string) bool {
	raw := make([]byte, 8)
	if _, err := io.ReadFull(s.reader, raw); err != nil {
		return false
	}
	size := int64(binary.LittleEndian.Uint64(raw))
	r.mutex.Lock()
	onData, wrongSum := r.OnData, r.WrongPutSum
	r.mutex.Unlock()
	os.MkdirAll(filepath.Dir(path), 0755)
	part := path + ttx.PartSuffix
	file, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		s.refuse("create: " + err.Error())
		return false
	}
	if err := file.Truncate(size); err != nil {
		file.Close()
		os.Remove(part)
		s.refuse("reserve space: " + err.Error())
		return false
	}
	s.ok()
	if s.writer.Flush() != nil {
		file.Close()
		os.Remove(part)
		return false
	}
	sum := crc32.NewIEEE()
	buffer := make([]byte, 64<<10)
	var written int64
	for written < size {
		s.conn.SetDeadline(time.Now().Add(30 * time.Second))
		n, err := s.reader.Read(buffer[:min(int64(len(buffer)), size-written)])
		if n > 0 {
			if _, werr := file.Write(buffer[:n]); werr != nil {
				file.Close()
				os.Remove(part)
				s.refuse("write: " + werr.Error())
				return false
			}
			sum.Write(buffer[:n])
			written += int64(n)
			if onData != nil {
				onData(shown, written)
			}
		}
		if err != nil && written < size {
			// cut: nothing of it stays
			file.Close()
			os.Remove(part)
			return false
		}
	}
	if err := file.Close(); err != nil {
		os.Remove(part)
		s.refuse("close: " + err.Error())
		return false
	}
	os.Remove(path)
	if err := os.Rename(part, path); err != nil {
		os.Remove(part)
		return s.refuse("rename: "+err.Error()) == nil
	}
	r.mutex.Lock()
	r.puts = append(r.puts, shown)
	r.mutex.Unlock()
	told := sum.Sum32()
	if wrongSum {
		told ^= 0xFFFFFFFF
	}
	s.ok()
	s.u32(told)
	return s.writer.Flush() == nil
}

func (r *Receiver) get(s *session, path string, send bool) bool {
	info, err := os.Stat(path)
	if err != nil {
		return s.refuse("stat: "+err.Error()) == nil
	}
	if info.IsDir() {
		return s.refuse("is a folder") == nil
	}
	file, err := os.Open(path)
	if err != nil {
		return s.refuse("open: "+err.Error()) == nil
	}
	defer file.Close()
	s.ok()
	s.u64(uint64(info.Size()))
	sum := crc32.NewIEEE()
	var out io.Writer = sum
	if send {
		out = io.MultiWriter(sum, s.writer)
	}
	if _, err := io.CopyN(out, file, info.Size()); err != nil {
		// the client was promised the bytes: all that is left is to hang up
		return false
	}
	s.u32(sum.Sum32())
	return s.writer.Flush() == nil
}

func (r *Receiver) remove(s *session, path string, shown string) bool {
	if strings.TrimRight(shown, "/") == "" {
		return s.refuse("not the whole card") == nil
	}
	if _, err := os.Stat(path); err != nil {
		return s.refuse("stat: "+err.Error()) == nil
	}
	// a folder only when it is empty, as rmdir does
	if err := os.Remove(path); err != nil {
		return s.refuse("delete: "+err.Error()) == nil
	}
	if s.ok() != nil {
		return false
	}
	return s.writer.Flush() == nil
}
