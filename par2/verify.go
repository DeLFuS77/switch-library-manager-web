package par2

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// Result is what checking a file against its recovery file found.
type Result struct {
	// the slices of the file whose checksums do not match, by number
	Damaged []int
	Slices  int
	// the recovery slices the recovery file holds whole: as many damaged slices can be rebuilt
	Recovery int
	// the length the file must have, and whether it has it
	Length      int64
	WrongLength bool
}

// OK tells whether the file is as its recovery file knows it.
func (r Result) OK() bool { return len(r.Damaged) == 0 && !r.WrongLength }

// Repairable tells whether the recovery file holds enough to rebuild the file.
func (r Result) Repairable() bool { return len(r.Damaged) <= r.Recovery }

// ErrTooDamaged is given when a file has more damaged slices than its recovery file can rebuild.
var ErrTooDamaged = errors.New("the file has more damaged slices than its recovery data can rebuild")

// readSet reads the packets of a recovery file. Each packet is taken only when its MD5 matches;
// the recovery slices are noted by where their data is, not read into memory.
func readSet(path string) (*parsedSet, *os.File, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	set := &parsedSet{}
	var mainOK bool
	var slice int64
	var mainID [16]byte
	descs := map[[16]byte]*fileDescription{}
	ifscs := map[[16]byte][]sliceCheck{}
	exponents := map[int]bool{}
	header := make([]byte, headerLength)
	size := info.Size()
	for offset := int64(0); offset+headerLength <= size; {
		if _, err := file.ReadAt(header, offset); err != nil {
			break
		}
		if string(header[:8]) != packetMagic {
			offset += 4
			continue
		}
		length := int64(binary.LittleEndian.Uint64(header[8:16]))
		if length < headerLength || length%4 != 0 || offset+length > size {
			offset += 4
			continue
		}
		// the MD5 of the packet, from its set ID to its end, read as a stream
		sum := md5.New()
		if _, err := io.Copy(sum, io.NewSectionReader(file, offset+32, length-32)); err != nil {
			return nil, nil, err
		}
		if !bytes.Equal(sum.Sum(nil), header[16:32]) {
			offset += 4
			continue
		}
		var setID [16]byte
		copy(setID[:], header[32:48])
		kind := header[48:64]
		bodyLength := length - headerLength
		switch {
		case bytes.Equal(kind, typeRecovery):
			if bodyLength >= 4 {
				exponent := make([]byte, 4)
				file.ReadAt(exponent, offset+headerLength)
				e := int(binary.LittleEndian.Uint32(exponent))
				if !exponents[e] {
					exponents[e] = true
					set.recovery = append(set.recovery, recoverySlice{exponent: e, offset: offset + headerLength + 4})
				}
			}
		case bodyLength <= 1<<26:
			body := make([]byte, bodyLength)
			if _, err := file.ReadAt(body, offset+headerLength); err != nil {
				break
			}
			switch {
			case bytes.Equal(kind, typeMain) && len(body) >= 12:
				if binary.LittleEndian.Uint32(body[8:12]) == 1 && len(body) >= 28 {
					slice = int64(binary.LittleEndian.Uint64(body[:8]))
					copy(mainID[:], body[12:28])
					mainOK = true
				}
			case bytes.Equal(kind, typeFileDesc) && len(body) >= 56:
				d := &fileDescription{setID: setID}
				copy(d.id[:], body[0:16])
				copy(d.md5[:], body[16:32])
				copy(d.md516k[:], body[32:48])
				d.length = int64(binary.LittleEndian.Uint64(body[48:56]))
				d.name = string(bytes.TrimRight(body[56:], "\x00"))
				descs[d.id] = d
			case bytes.Equal(kind, typeIFSC) && len(body) >= 16 && (len(body)-16)%20 == 0:
				var id [16]byte
				copy(id[:], body[:16])
				checks := []sliceCheck{}
				for i := 16; i < len(body); i += 20 {
					var c sliceCheck
					copy(c.md5[:], body[i:i+16])
					c.crc32 = binary.LittleEndian.Uint32(body[i+16 : i+20])
					checks = append(checks, c)
				}
				ifscs[id] = checks
			}
		}
		offset += length
	}
	if !mainOK || slice <= 0 || slice%4 != 0 {
		file.Close()
		return nil, nil, errNoSet
	}
	d := descs[mainID]
	checks := ifscs[mainID]
	if d == nil || checks == nil || int64(len(checks)) != (d.length+slice-1)/slice {
		file.Close()
		return nil, nil, errNoSet
	}
	d.slice, d.checks = slice, checks
	set.file = d
	return set, file, nil
}

// Verify checks file against its recovery file, slice by slice.
func Verify(ctx context.Context, recoveryPath string, file string, progress func(done, total int64)) (Result, error) {
	set, recovery, err := readSet(recoveryPath)
	if err != nil {
		return Result{}, err
	}
	defer recovery.Close()
	return verifyWith(ctx, set, file, progress)
}

func verifyWith(ctx context.Context, set *parsedSet, file string, progress func(done, total int64)) (Result, error) {
	f := set.file
	result := Result{Slices: len(f.checks), Recovery: len(set.recovery), Length: f.length}
	input, err := os.Open(file)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	result.WrongLength = info.Size() != f.length
	buffer := make([]byte, f.slice)
	for i, check := range f.checks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		offset := int64(i) * f.slice
		// what the file holds of the slice, as long as the file it should be
		n, err := readSlice(input, buffer, offset, min(f.length, info.Size()))
		if err != nil {
			return result, err
		}
		if md5.Sum(buffer) != check.md5 || crc32.ChecksumIEEE(buffer) != check.crc32 {
			result.Damaged = append(result.Damaged, i)
		}
		if progress != nil {
			progress(offset+int64(n), f.length)
		}
	}
	return result, nil
}

// Repair rebuilds the damaged slices of file with its recovery file, in place, and gives what
// was found before. The bytes it replaces are kept in memory: if the file is not whole once
// rebuilt (its MD5 checked against the recovery file), they are put back, and it is left as it
// was. A file with more damaged slices than recovery slices is left as it is (ErrTooDamaged).
func Repair(ctx context.Context, recoveryPath string, file string, progress func(done, total int64)) (Result, error) {
	set, recovery, err := readSet(recoveryPath)
	if err != nil {
		return Result{}, err
	}
	defer recovery.Close()
	result, err := verifyWith(ctx, set, file, progress)
	if err != nil || result.OK() {
		return result, err
	}
	if !result.Repairable() {
		return result, ErrTooDamaged
	}
	f := set.file
	damaged := result.Damaged
	m := len(damaged)
	logs := inputLogs(len(f.checks))
	used := set.recovery[:m]

	// the equations: each recovery slice, less what the slices that are whole give it, is the
	// sum of the damaged slices times their constants
	matrix := make([][]uint16, m)
	for k, r := range used {
		matrix[k] = make([]uint16, m)
		for j, d := range damaged {
			matrix[k][j] = coefficient(logs[d], r.exponent)
		}
	}
	inverse, ok := invert(matrix)
	if !ok {
		return result, errors.New("the recovery slices cannot rebuild these slices")
	}
	sums := make([][]byte, m)
	for k, r := range used {
		sums[k] = make([]byte, f.slice)
		if _, err := recovery.ReadAt(sums[k], r.offset); err != nil {
			return result, fmt.Errorf("the recovery file cannot be read: %w", err)
		}
	}
	input, err := os.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	isDamaged := map[int]bool{}
	for _, d := range damaged {
		isDamaged[d] = true
	}
	buffer := make([]byte, f.slice)
	for i := range f.checks {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if isDamaged[i] {
			continue
		}
		if _, err := readSlice(input, buffer, int64(i)*f.slice, min(f.length, info.Size())); err != nil {
			return result, err
		}
		for k, r := range used {
			newMulTable(coefficient(logs[i], r.exponent)).mulAdd(sums[k], buffer)
		}
	}
	rebuilt := make([][]byte, m)
	for j := range damaged {
		rebuilt[j] = make([]byte, f.slice)
		for k := range used {
			newMulTable(inverse[j][k]).mulAdd(rebuilt[j], sums[k])
		}
		if md5.Sum(rebuilt[j]) != f.checks[damaged[j]].md5 {
			return result, errors.New("a slice rebuilt does not match its checksum: the recovery data does not fit this file")
		}
	}

	// the bytes replaced are kept, to be put back if the file is not whole after all
	type kept struct {
		offset int64
		data   []byte
	}
	before := []kept{}
	originalSize := info.Size()
	restore := func() {
		for _, k := range before {
			input.WriteAt(k.data, k.offset)
		}
		input.Truncate(originalSize)
	}
	for j, d := range damaged {
		offset := int64(d) * f.slice
		length := min(f.slice, f.length-offset)
		if offset < originalSize {
			old := make([]byte, min(length, originalSize-offset))
			input.ReadAt(old, offset)
			before = append(before, kept{offset: offset, data: old})
		}
		if _, err := input.WriteAt(rebuilt[j][:length], offset); err != nil {
			restore()
			return result, err
		}
	}
	if originalSize > f.length {
		if err := input.Truncate(f.length); err != nil {
			restore()
			return result, err
		}
	}
	whole := md5.New()
	if _, err := io.Copy(whole, io.NewSectionReader(input, 0, f.length)); err != nil || !bytes.Equal(whole.Sum(nil), f.md5[:]) {
		restore()
		if err == nil {
			err = errors.New("the file is not whole once rebuilt: it is left as it was")
		}
		return result, err
	}
	return result, input.Sync()
}

// Describe tells how many slices the file of a recovery file has, and how many recovery slices
// it holds whole.
func Describe(recoveryPath string) (slices int, recovery int, err error) {
	set, file, err := readSet(recoveryPath)
	if err != nil {
		return 0, 0, err
	}
	file.Close()
	return len(set.file.checks), len(set.recovery), nil
}

// Checker checks the slices of a file against its recovery file one by one, as the file is read
// for something else: what reads it can tell a damaged slice before it uses it.
type Checker struct {
	file *fileDescription
}

// NewChecker reads the checksums of a recovery file.
func NewChecker(recoveryPath string) (*Checker, error) {
	set, file, err := readSet(recoveryPath)
	if err != nil {
		return nil, err
	}
	file.Close()
	return &Checker{file: set.file}, nil
}

// SliceSize is the size of the slices of the file.
func (c *Checker) SliceSize() int64 { return c.file.slice }

// Length is the length the file must have.
func (c *Checker) Length() int64 { return c.file.length }

// ReadSlice reads slice index of input as the checksums take it (padded with zeros past the end
// of the file) into buffer, of SliceSize bytes, and tells whether it is whole.
func (c *Checker) ReadSlice(input io.ReaderAt, index int, buffer []byte) (bool, error) {
	if index < 0 || index >= len(c.file.checks) || int64(len(buffer)) != c.file.slice {
		return false, errors.New("no such slice")
	}
	if _, err := readSlice(input, buffer, int64(index)*c.file.slice, c.file.length); err != nil {
		return false, err
	}
	check := c.file.checks[index]
	return md5.Sum(buffer) == check.md5 && crc32.ChecksumIEEE(buffer) == check.crc32, nil
}
