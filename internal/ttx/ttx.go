// Package ttx is a client of the "TTX1" protocol, with which nx2-flow (a menu of Switch 2 homebrew)
// receives files from a computer over the local network on its Transfer screen.
//
// The protocol, as its receiver documents it and behaves:
//
//   - TCP, port 28302 by default; numbers are little-endian; one connection carries any number of
//     requests, one after another.
//   - A request is "TTX1", u8 op, u16 length of the path, the path: UTF-8, starting with "/",
//     relative to the root of the card, without "..", "\" or ":". The receiver hangs up on a
//     request that is not one, or on a path longer than 1024 bytes.
//   - A reply starts with u8 status: 0 is success and its body follows; anything else is followed
//     by u16 length and a message, and no body.
//   - PING (0): u64 free, u64 total (bytes of the card), u16 length and the text of its version.
//   - LIST (1): u32 count, u32 bytes of what follows, then for each entry u8 is_dir, u64 size,
//     u64 mtime (Unix seconds), u16 length and its name.
//   - PUT (2): the request has u64 size after the path. The receiver makes the folders on the way,
//     creates "<path>.ttpart" with that size and replies with a status; only then are the size
//     bytes sent. Once every byte arrived it renames the part file over the path and replies
//     with a status and u32 CRC-32 (zlib's) of the bytes it wrote to the card.
//   - GET (3): u64 size, the bytes, u32 CRC-32 of what it read.
//   - DELETE (4): a file or an empty folder; no body.
//   - HASH (5): u64 size and u32 CRC-32, read back from the card.
//   - MKDIR (6): the folder with every folder on the way; no body.
//
// After an error in the middle of a PUT or a GET the receiver hangs up, and a PUT cut short leaves
// no file: the part file is removed. The connection of a Client is closed after such an error.
package ttx

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// DefaultPort is the port the receiver listens on.
const DefaultPort = 28302

// Magic starts every request.
const Magic = "TTX1"

// The operations of a request.
const (
	OpPing   byte = 0
	OpList   byte = 1
	OpPut    byte = 2
	OpGet    byte = 3
	OpDelete byte = 4
	OpHash   byte = 5
	OpMkdir  byte = 6
)

// MaxPath is the longest path the receiver takes, in bytes.
const MaxPath = 1024

// PartSuffix is what the receiver adds to the name of a file while it arrives.
const PartSuffix = ".ttpart"

const (
	// how long a step may wait for the other side by default: the receiver gives up on a
	// connection after 30 seconds of nothing
	defaultIdle = 30 * time.Second
	// the block a PUT sends at a time
	putBlock = 1 << 20
	// the slowest pace a card is expected to be read back at, for the time a HASH may take
	hashPace = 4 << 20
)

var (
	// ErrBadPath is a path the receiver would refuse.
	ErrBadPath = errors.New("the path is not one the receiver takes")
	// ErrMismatch is a file that did not arrive as it was sent.
	ErrMismatch = errors.New("the file on the card is not the one sent")
	// ErrShortSource is a source that ended before the size it was sent with.
	ErrShortSource = errors.New("the file to send ended before its size")
	// ErrClosed is a client used after its connection was closed.
	ErrClosed = errors.New("the connection is closed")
)

// RemoteError is a request the receiver refused, with the reason it gave.
type RemoteError struct {
	Op      string
	Path    string
	Status  byte
	Message string
}

func (e *RemoteError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s", e.Op, e.Message)
	}
	return fmt.Sprintf("%s %s: %s", e.Op, e.Path, e.Message)
}

// Info is what PING tells.
type Info struct {
	Free, Total uint64
	Version     string
}

// Entry is a file or folder LIST tells.
type Entry struct {
	Name     string
	Dir      bool
	Size     uint64
	Modified time.Time
}

// CheckPath tells whether the receiver takes a path.
func CheckPath(path string) error {
	if path == "" || path[0] != '/' || len(path) > MaxPath || strings.Contains(path, "..") || strings.ContainsAny(path, "\\:\x00") {
		return fmt.Errorf("%w: %q", ErrBadPath, path)
	}
	return nil
}

// Client is a connection to a receiver. Its methods are not to be called at the same time; Close
// may be, to cut what goes on.
type Client struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	// how long a step may wait for the other side
	Idle   time.Duration
	mutex  sync.Mutex
	closed bool
}

// Dial connects to a receiver at address (host:port), waiting at most timeout.
func Dial(ctx context.Context, address string, timeout time.Duration) (*Client, error) {
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	return NewClient(conn), nil
}

// NewClient is a client on a connection already made.
func NewClient(conn net.Conn) *Client {
	if tcp, ok := conn.(*net.TCPConn); ok {
		tcp.SetNoDelay(true)
		tcp.SetWriteBuffer(4 << 20)
	}
	return &Client{conn: conn, reader: bufio.NewReaderSize(conn, 64<<10), writer: bufio.NewWriterSize(conn, putBlock), Idle: defaultIdle}
}

// Close closes the connection.
func (c *Client) Close() error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

func (c *Client) isClosed() bool {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.closed
}

// watch cuts the connection when ctx ends; the function returned stops watching.
func (c *Client) watch(ctx context.Context) func() bool {
	return context.AfterFunc(ctx, func() { c.Close() })
}

// fail is the error a step ended with: the end of ctx when that is what cut it. A connection that
// failed is closed, as what it carries is no longer known.
func (c *Client) fail(ctx context.Context, err error) error {
	c.Close()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (c *Client) deadline(wait time.Duration) {
	c.conn.SetDeadline(time.Now().Add(wait))
}

func (c *Client) idle() time.Duration {
	if c.Idle <= 0 {
		return defaultIdle
	}
	return c.Idle
}

// request sends the head of a request, with extra after the path, without flushing it.
func (c *Client) request(op byte, path string, extra []byte) error {
	head := make([]byte, 0, 7+len(path)+len(extra))
	head = append(head, Magic...)
	head = append(head, op)
	head = binary.LittleEndian.AppendUint16(head, uint16(len(path)))
	head = append(head, path...)
	head = append(head, extra...)
	c.deadline(c.idle())
	_, err := c.writer.Write(head)
	return err
}

// status reads the status of a reply: nil for success, else the RemoteError it tells.
func (c *Client) status(name string, path string) error {
	code, err := c.reader.ReadByte()
	if err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	length, err := c.u16()
	if err != nil {
		return err
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(c.reader, message); err != nil {
		return err
	}
	return &RemoteError{Op: name, Path: path, Status: code, Message: string(message)}
}

func (c *Client) u16() (uint16, error) {
	var b [2]byte
	_, err := io.ReadFull(c.reader, b[:])
	return binary.LittleEndian.Uint16(b[:]), err
}

func (c *Client) u32() (uint32, error) {
	var b [4]byte
	_, err := io.ReadFull(c.reader, b[:])
	return binary.LittleEndian.Uint32(b[:]), err
}

func (c *Client) u64() (uint64, error) {
	var b [8]byte
	_, err := io.ReadFull(c.reader, b[:])
	return binary.LittleEndian.Uint64(b[:]), err
}

func (c *Client) text() (string, error) {
	length, err := c.u16()
	if err != nil {
		return "", err
	}
	b := make([]byte, length)
	_, err = io.ReadFull(c.reader, b)
	return string(b), err
}

// simple sends a request without a body and reads its status: for DELETE and MKDIR. A refusal
// leaves the connection usable.
func (c *Client) simple(ctx context.Context, op byte, name string, path string) error {
	if err := CheckPath(path); err != nil {
		return err
	}
	if c.isClosed() {
		return ErrClosed
	}
	defer c.watch(ctx)()
	if err := c.request(op, path, nil); err != nil {
		return c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return c.fail(ctx, err)
	}
	if err := c.status(name, path); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			return err
		}
		return c.fail(ctx, err)
	}
	return nil
}

// Ping asks the receiver how much room the card has and which version it is.
func (c *Client) Ping(ctx context.Context) (Info, error) {
	if c.isClosed() {
		return Info{}, ErrClosed
	}
	defer c.watch(ctx)()
	var info Info
	if err := c.request(OpPing, "", nil); err != nil {
		return info, c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return info, c.fail(ctx, err)
	}
	if err := c.status("PING", ""); err != nil {
		return info, c.fail(ctx, err)
	}
	var err error
	if info.Free, err = c.u64(); err == nil {
		if info.Total, err = c.u64(); err == nil {
			info.Version, err = c.text()
		}
	}
	if err != nil {
		return Info{}, c.fail(ctx, err)
	}
	return info, nil
}

// Mkdir makes a folder of the card, with every folder on the way.
func (c *Client) Mkdir(ctx context.Context, path string) error {
	return c.simple(ctx, OpMkdir, "MKDIR", path)
}

// Delete deletes a file, or an empty folder, of the card.
func (c *Client) Delete(ctx context.Context, path string) error {
	return c.simple(ctx, OpDelete, "DELETE", path)
}

// List tells what a folder of the card holds.
func (c *Client) List(ctx context.Context, path string) ([]Entry, error) {
	if err := CheckPath(path); err != nil {
		return nil, err
	}
	if c.isClosed() {
		return nil, ErrClosed
	}
	defer c.watch(ctx)()
	if err := c.request(OpList, path, nil); err != nil {
		return nil, c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return nil, c.fail(ctx, err)
	}
	if err := c.status("LIST", path); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			return nil, err
		}
		return nil, c.fail(ctx, err)
	}
	count, err := c.u32()
	if err != nil {
		return nil, c.fail(ctx, err)
	}
	size, err := c.u32()
	if err != nil {
		return nil, c.fail(ctx, err)
	}
	// the entries are read as the size told: each holds at least 19 bytes
	if uint64(count)*19 > uint64(size) || size > 64<<20 {
		return nil, c.fail(ctx, fmt.Errorf("LIST %s: %d entries cannot be in %d bytes", path, count, size))
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(c.reader, data); err != nil {
		return nil, c.fail(ctx, err)
	}
	entries := make([]Entry, 0, count)
	for i := uint32(0); i < count; i++ {
		if len(data) < 19 {
			return nil, c.fail(ctx, fmt.Errorf("LIST %s: the entries end early", path))
		}
		entry := Entry{Dir: data[0] != 0, Size: binary.LittleEndian.Uint64(data[1:9]),
			Modified: time.Unix(int64(binary.LittleEndian.Uint64(data[9:17])), 0)}
		length := int(binary.LittleEndian.Uint16(data[17:19]))
		data = data[19:]
		if len(data) < length {
			return nil, c.fail(ctx, fmt.Errorf("LIST %s: the entries end early", path))
		}
		entry.Name, data = string(data[:length]), data[length:]
		entries = append(entries, entry)
	}
	return entries, nil
}

// Hash is the size and CRC-32 of a file of the card, read back from the card. Reading a large
// file takes a while, which is waited for.
func (c *Client) Hash(ctx context.Context, path string) (uint64, uint32, error) {
	if err := CheckPath(path); err != nil {
		return 0, 0, err
	}
	if c.isClosed() {
		return 0, 0, ErrClosed
	}
	defer c.watch(ctx)()
	if err := c.request(OpHash, path, nil); err != nil {
		return 0, 0, c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return 0, 0, c.fail(ctx, err)
	}
	if err := c.status("HASH", path); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			return 0, 0, err
		}
		return 0, 0, c.fail(ctx, err)
	}
	size, err := c.u64()
	if err != nil {
		return 0, 0, c.fail(ctx, err)
	}
	// the sum comes once the whole file was read from the card
	c.deadline(c.idle() + time.Duration(size/hashPace)*time.Second)
	sum, err := c.u32()
	if err != nil {
		return 0, 0, c.fail(ctx, err)
	}
	return size, sum, nil
}

// Get reads a file of the card into w, at most limit bytes (else it is refused before anything is
// read), and checks it against the sum the receiver tells. It gives the size of the file.
func (c *Client) Get(ctx context.Context, path string, w io.Writer, limit int64) (int64, error) {
	if err := CheckPath(path); err != nil {
		return 0, err
	}
	if c.isClosed() {
		return 0, ErrClosed
	}
	defer c.watch(ctx)()
	if err := c.request(OpGet, path, nil); err != nil {
		return 0, c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return 0, c.fail(ctx, err)
	}
	if err := c.status("GET", path); err != nil {
		var remote *RemoteError
		if errors.As(err, &remote) {
			return 0, err
		}
		return 0, c.fail(ctx, err)
	}
	size, err := c.u64()
	if err != nil {
		return 0, c.fail(ctx, err)
	}
	if limit >= 0 && size > uint64(limit) {
		// the receiver sends it all the same: the connection is given up
		return 0, c.fail(ctx, fmt.Errorf("GET %s: %d bytes, more than %d", path, size, limit))
	}
	sum := crc32.NewIEEE()
	left := int64(size)
	buffer := make([]byte, 256<<10)
	for left > 0 {
		c.deadline(c.idle())
		n, err := c.reader.Read(buffer[:min(int64(len(buffer)), left)])
		if n > 0 {
			sum.Write(buffer[:n])
			if _, werr := w.Write(buffer[:n]); werr != nil {
				return 0, c.fail(ctx, werr)
			}
			left -= int64(n)
		}
		if err != nil && left > 0 {
			return 0, c.fail(ctx, err)
		}
	}
	told, err := c.u32()
	if err != nil {
		return 0, c.fail(ctx, err)
	}
	if told != sum.Sum32() {
		return int64(size), fmt.Errorf("GET %s: %w", path, ErrMismatch)
	}
	return int64(size), nil
}

// Put sends size bytes of source to path on the card, telling progress (if not nil) how many were
// sent so far. The receiver writes them to a part file it renames over path only when all of them
// arrived; the CRC-32 it tells of what it wrote is checked against the one of what was sent, and
// returned. Ending ctx cuts the connection, and the receiver removes the part file.
func (c *Client) Put(ctx context.Context, path string, source io.Reader, size int64, progress func(sent int64)) (uint32, error) {
	if err := CheckPath(path); err != nil {
		return 0, err
	}
	if size < 0 {
		return 0, fmt.Errorf("PUT %s: a size below zero", path)
	}
	if c.isClosed() {
		return 0, ErrClosed
	}
	defer c.watch(ctx)()
	if err := c.request(OpPut, path, binary.LittleEndian.AppendUint64(nil, uint64(size))); err != nil {
		return 0, c.fail(ctx, err)
	}
	if err := c.writer.Flush(); err != nil {
		return 0, c.fail(ctx, err)
	}
	// the receiver gives the file its whole size before it answers; it hangs up after refusing a
	// PUT, as the data could not be told from requests
	c.deadline(2 * c.idle())
	if err := c.status("PUT", path); err != nil {
		return 0, c.fail(ctx, err)
	}
	sum := crc32.NewIEEE()
	buffer := make([]byte, putBlock)
	var sent int64
	for sent < size {
		want := min(int64(len(buffer)), size-sent)
		n, err := io.ReadFull(source, buffer[:want])
		if n > 0 {
			sum.Write(buffer[:n])
			c.deadline(c.idle())
			if _, werr := c.writer.Write(buffer[:n]); werr != nil {
				return 0, c.fail(ctx, werr)
			}
			sent += int64(n)
			if progress != nil {
				progress(sent)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				err = ErrShortSource
			}
			return 0, c.fail(ctx, fmt.Errorf("PUT %s: %w", path, err))
		}
	}
	c.deadline(c.idle())
	if err := c.writer.Flush(); err != nil {
		return 0, c.fail(ctx, err)
	}
	// closing and renaming the file on the card may take a moment
	c.deadline(2 * c.idle())
	if err := c.status("PUT", path); err != nil {
		// a write that failed on the card hangs the receiver up too
		return 0, c.fail(ctx, err)
	}
	told, err := c.u32()
	if err != nil {
		return 0, c.fail(ctx, err)
	}
	if told != sum.Sum32() {
		return told, fmt.Errorf("PUT %s: %w (sent %08x, written %08x)", path, ErrMismatch, sum.Sum32(), told)
	}
	return told, nil
}
