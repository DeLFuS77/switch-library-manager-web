// Package par2 writes, checks and uses recovery data in the PAR 2.0 format for one file: a file
// "<name>.par2" next to it, which par2cmdline, MultiPar and the other PAR 2.0 tools read too. It
// holds the checksums of the file, slice by slice, and recovery slices that rebuild as many
// damaged slices as there are recovery slices, wherever they are in the file.
//
// The file is read as a stream, never whole in memory: the recovery slices are computed in
// passes over the file that keep at most MemoryBudget bytes of them at once.
package par2

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
)

const (
	packetMagic  = "PAR2\x00PKT"
	headerLength = 64
	// the slices a file is cut into, about: fewer, longer slices keep the work and the memory
	// low, as each recovery slice is a sum of every input slice
	targetSlices = 2000
	maxSlices    = 32768
	minSlice     = 4096
	first16k     = 16 * 1024
)

var (
	typeMain     = []byte("PAR 2.0\x00Main\x00\x00\x00\x00")
	typeFileDesc = []byte("PAR 2.0\x00FileDesc")
	typeIFSC     = []byte("PAR 2.0\x00IFSC\x00\x00\x00\x00")
	typeRecovery = []byte("PAR 2.0\x00RecvSlic")
	typeCreator  = []byte("PAR 2.0\x00Creator\x00")
)

// MemoryBudget is the most memory the recovery slices of one pass take.
var MemoryBudget int64 = 256 << 20

// Path is the recovery file of a file: its name with .par2 added.
func Path(file string) string {
	return file + ".par2"
}

// IsRecoveryFile tells whether a path is a PAR 2.0 file.
func IsRecoveryFile(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".par2")
}

// sliceSize is the size of the slices of a file of size bytes: a multiple of 4.
func sliceSize(size int64) int64 {
	slice := (size + targetSlices - 1) / targetSlices
	if slice < minSlice {
		slice = minSlice
	}
	for (size+slice-1)/slice > maxSlices {
		slice *= 2
	}
	return (slice + 3) &^ 3
}

// recoveryCount is how many recovery slices percent of slices gives: at least one.
func recoveryCount(slices int, percent float64) int {
	count := int(float64(slices)*percent/100 + 0.999999)
	if count < 1 {
		count = 1
	}
	if count > maxSlices {
		count = maxSlices
	}
	return count
}

// pad4 pads b with zeros to a multiple of 4 bytes.
func pad4(b []byte) []byte {
	if extra := len(b) % 4; extra != 0 {
		b = append(b, make([]byte, 4-extra)...)
	}
	return b
}

// packet is a whole packet of the set setID, of its type and body.
func packet(setID [16]byte, kind []byte, body []byte) []byte {
	p := make([]byte, headerLength+len(body))
	copy(p, packetMagic)
	binary.LittleEndian.PutUint64(p[8:], uint64(len(p)))
	copy(p[32:], setID[:])
	copy(p[48:], kind)
	copy(p[64:], body)
	sum := md5.Sum(p[32:])
	copy(p[16:], sum[:])
	return p
}

// fileDescription is what a set knows of its file.
type fileDescription struct {
	id      [16]byte
	md5     [16]byte
	md516k  [16]byte
	length  int64
	name    string
	checks  []sliceCheck
	slice   int64
	setID   [16]byte
	present bool
}

type sliceCheck struct {
	md5   [16]byte
	crc32 uint32
}

func fileID(md516k [16]byte, length int64, name string) [16]byte {
	b := make([]byte, 0, 16+8+len(name))
	b = append(b, md516k[:]...)
	b = binary.LittleEndian.AppendUint64(b, uint64(length))
	b = append(b, name...)
	return md5.Sum(b)
}

func mainBody(slice int64, id [16]byte) []byte {
	body := make([]byte, 12, 12+16)
	binary.LittleEndian.PutUint64(body, uint64(slice))
	binary.LittleEndian.PutUint32(body[8:], 1)
	return append(body, id[:]...)
}

func fileDescBody(f *fileDescription) []byte {
	body := make([]byte, 0, 56+len(f.name)+3)
	body = append(body, f.id[:]...)
	body = append(body, f.md5[:]...)
	body = append(body, f.md516k[:]...)
	body = binary.LittleEndian.AppendUint64(body, uint64(f.length))
	return pad4(append(body, f.name...))
}

func ifscBody(f *fileDescription) []byte {
	body := make([]byte, 0, 16+20*len(f.checks))
	body = append(body, f.id[:]...)
	for _, c := range f.checks {
		body = append(body, c.md5[:]...)
		body = binary.LittleEndian.AppendUint32(body, c.crc32)
	}
	return body
}

// criticalPackets are the packets a set needs to check its file: its main packet, the
// description of the file and the checksums of its slices.
func criticalPackets(f *fileDescription) []byte {
	var out bytes.Buffer
	out.Write(packet(f.setID, typeMain, mainBody(f.slice, f.id)))
	out.Write(packet(f.setID, typeFileDesc, fileDescBody(f)))
	out.Write(packet(f.setID, typeIFSC, ifscBody(f)))
	return out.Bytes()
}

// parsedSet is what a recovery file holds, read packet by packet: the packets whose MD5 does
// not match are left out, so a damaged recovery file still gives what it has left.
type parsedSet struct {
	file     *fileDescription
	recovery []recoverySlice
}

type recoverySlice struct {
	exponent int
	offset   int64 // of its data in the recovery file
}

var errNoSet = errors.New("the recovery file holds no recovery set for one file")
