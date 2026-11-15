package par2

import (
	"context"
	"crypto/md5"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// Creator is the name the recovery files tell they were written by.
var Creator = "NXPress"

// Create writes the recovery file of file at target: its checksums, and recovery slices for
// percent of its slices (at least one). progress, if given, follows the bytes read; the file is
// read once per pass (see MemoryBudget). The target is written under another name and renamed,
// so a recovery file is never left half written.
func Create(ctx context.Context, file string, target string, percent float64, progress func(done, total int64)) error {
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	size := info.Size()
	if size == 0 {
		return errors.New("an empty file needs no recovery data")
	}
	f := &fileDescription{length: size, name: filepath.Base(file), slice: sliceSize(size)}
	slices := int((size + f.slice - 1) / f.slice)
	recovery := recoveryCount(slices, percent)
	logs := inputLogs(slices)

	// the set is named by its main packet, which names the file by the MD5 of its first 16 KiB
	head := make([]byte, min(size, first16k))
	if _, err := input.ReadAt(head, 0); err != nil {
		return err
	}
	f.md516k = md5.Sum(head)
	f.id = fileID(f.md516k, f.length, f.name)
	f.setID = md5.Sum(mainBody(f.slice, f.id))

	written := target + ".tmp"
	output, err := os.Create(written)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			output.Close()
			os.Remove(written)
		}
	}()
	creator := packet(f.setID, typeCreator, pad4([]byte(Creator)))
	if _, err := output.Write(creator); err != nil {
		return err
	}

	perPass := int(max(1, MemoryBudget/f.slice))
	passes := (recovery + perPass - 1) / perPass
	total := size * int64(passes)
	var done int64
	for first := 0; first < recovery; first += perPass {
		count := min(perPass, recovery-first)
		blocks, checks, whole, err := encodePass(ctx, input, f, logs, first, count, first == 0, func(n int64) {
			done += n
			if progress != nil {
				progress(done, total)
			}
		})
		if err != nil {
			return err
		}
		if first == 0 {
			f.checks, f.md5 = checks, whole
		}
		for i, block := range blocks {
			body := make([]byte, 4, 4+len(block))
			putUint32(body, uint32(first+i))
			if _, err := output.Write(packet(f.setID, typeRecovery, append(body, block...))); err != nil {
				return err
			}
		}
	}
	// the packets that check the file, twice: one copy damaged leaves the other
	critical := criticalPackets(f)
	if _, err := output.Write(critical); err != nil {
		return err
	}
	if _, err := output.Write(critical); err != nil {
		return err
	}
	if _, err := output.Write(creator); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(written, target); err != nil {
		os.Remove(written)
		return err
	}
	ok = true
	return nil
}

// encodePass reads the whole file and gives the recovery slices first to first+count, and on
// the first pass the checksums of its slices and of the whole file.
func encodePass(ctx context.Context, input io.ReaderAt, f *fileDescription, logs []int, first, count int, checksums bool, read func(int64)) ([][]byte, []sliceCheck, [16]byte, error) {
	blocks := make([][]byte, count)
	for i := range blocks {
		blocks[i] = make([]byte, f.slice)
	}
	var checks []sliceCheck
	whole := md5.New()
	workers := min(count, runtime.NumCPU())
	// two buffers: one slice is read while the one before is added in
	buffers := [2][]byte{make([]byte, f.slice), make([]byte, f.slice)}
	type job struct {
		data  []byte
		index int
	}
	var adding sync.WaitGroup
	add := func(j job) {
		var wait sync.WaitGroup
		for w := 0; w < workers; w++ {
			wait.Add(1)
			go func(w int) {
				defer wait.Done()
				for b := w; b < count; b += workers {
					newMulTable(coefficient(logs[j.index], first+b)).mulAdd(blocks[b], j.data)
				}
			}(w)
		}
		wait.Wait()
	}
	slices := len(logs)
	for index := 0; index < slices; index++ {
		if err := ctx.Err(); err != nil {
			adding.Wait()
			return nil, nil, [16]byte{}, err
		}
		buffer := buffers[index%2]
		n, err := readSlice(input, buffer, int64(index)*f.slice, f.length)
		if err != nil {
			adding.Wait()
			return nil, nil, [16]byte{}, err
		}
		if checksums {
			whole.Write(buffer[:n])
			checks = append(checks, sliceCheck{md5: md5.Sum(buffer), crc32: crc32.ChecksumIEEE(buffer)})
		}
		read(int64(n))
		adding.Wait()
		adding.Add(1)
		go func(j job) {
			defer adding.Done()
			add(j)
		}(job{data: buffer, index: index})
	}
	adding.Wait()
	var sum [16]byte
	copy(sum[:], whole.Sum(nil))
	return blocks, checks, sum, nil
}

// readSlice reads the slice at offset into buffer, padded with zeros past the end of the file
// (length), and gives how many bytes of the file it holds.
func readSlice(input io.ReaderAt, buffer []byte, offset int64, length int64) (int, error) {
	n := 0
	if offset < length {
		want := int(min(int64(len(buffer)), length-offset))
		read, err := input.ReadAt(buffer[:want], offset)
		// a file shorter than it should be gives what it has: the rest is zeros, a damaged slice
		if err != nil && !errors.Is(err, io.EOF) {
			return 0, err
		}
		n = read
	}
	clear(buffer[n:])
	return n, nil
}

func putUint32(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
