package switchfs

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
)

// The check of a NUP is the one of NXPress: every piece of data read once, in the order it is
// stored, and the SHA-256 of each file, decompressed when stored with zstd, compared with the
// one its table of contents keeps. The code modules of each ExeFS are read as their loader
// reads them as they go by (nso.go): the hashes of a NUP are of the files as they were packed,
// and a dump that lost bytes of its code gives a NUP whose hashes match but whose game does not
// start.

const (
	// the data handed to a worker at once
	nupChunkSize = 1 << 20
	// the data of small files read at once: the many small files of a game are stored one after
	// the other, and are read in a few big reads instead of one each
	nupWindowSize = 8 << 20
	// the chunks waiting for each worker
	nupQueued = 4
	// the window of zstd a file may need to be decompressed: level 3, which the packer of the
	// format uses, needs 2 MB, the highest level 128 MB. A file asking for more is told as one
	// that cannot be decompressed, instead of taking the memory of the server.
	nupMaxWindow = 128 << 20
)

// NupDamaged is a file of a NUP whose data is not what its table of contents says.
type NupDamaged struct {
	// the package it is in ("game v65536", "update v131072", "DLC 0400...3001 v0"), "" for a
	// file in no folder of any package
	Package string
	// its path inside the package: romfs/Data/stage.bin
	Path   string
	Reason string
	// a code module of an ExeFS whose data is as it was packed, but that its own header tells
	// damaged: the dump the NUP was made of lacks bytes of it
	Code bool
}

func (d NupDamaged) String() string {
	if d.Package == "" {
		return d.Path + ": " + d.Reason
	}
	return d.Package + "/" + d.Path + ": " + d.Reason
}

// NupCodeDamaged starts the error of a NUP whose files are whole but whose code is damaged.
const NupCodeDamaged = "the code of the game is damaged in the dump this NUP was made of: dump the game again"

// NupDamagedError is a NUP with damaged files: the first of them, and how many there are.
type NupDamagedError struct {
	Damaged []NupDamaged
}

// CodeOnly tells a NUP whose files are all as they were packed, but whose code is damaged.
func (e *NupDamagedError) CodeOnly() bool {
	for _, d := range e.Damaged {
		if !d.Code {
			return false
		}
	}
	return len(e.Damaged) > 0
}

func (e *NupDamagedError) Error() string {
	if e.CodeOnly() {
		if len(e.Damaged) == 1 {
			return fmt.Sprintf("%s (%s)", NupCodeDamaged, e.Damaged[0])
		}
		return fmt.Sprintf("%s (%s, and %d more damaged modules)", NupCodeDamaged, e.Damaged[0], len(e.Damaged)-1)
	}
	// the damaged data first: the code is told damaged by its data
	first := e.Damaged[0]
	for _, d := range e.Damaged {
		if !d.Code {
			first = d
			break
		}
	}
	if len(e.Damaged) == 1 {
		return first.String()
	}
	return fmt.Sprintf("%s (and %d more damaged files)", first, len(e.Damaged)-1)
}

// nupExtent is a piece of data of the NUP to read and check, with the files it is the data of.
type nupExtent struct {
	offset, stored, size uint64
	codec                byte
	files                []int
	// why the data is damaged, "" when it is whole; and its SHA-256, once read whole
	reason string
	sum    [32]byte
	// the data of a code module, and what its header tells wrong with it
	code        bool
	codeProblem string
}

// nupFeed is the data of one piece, handed by the reader to a worker chunk after chunk.
type nupFeed struct {
	job    *nupExtent
	chunks chan []byte
}

// VerifyNup checks every file of a NUP against the SHA-256 its table of contents keeps, the
// table itself first by its hash. A reader goes through the file in order, as a hard disk reads
// fastest, and workers hash and decompress what it reads: as many as the threads of ctx
// (WithThreads), else every processor but a few. A file that cannot be read stops the check with
// the error of the read; damaged files give a NupDamagedError. progress is told the bytes read
// of the ones to read.
func VerifyNup(ctx context.Context, path string, progress func(done int64, total int64)) (FileCheck, error) {
	check := FileCheck{}
	file, err := os.Open(path)
	if err != nil {
		return check, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return check, err
	}
	container, err := readNup(file, info.Size())
	if err != nil {
		return check, err
	}

	// the code modules of the ExeFS of each package
	code := make([]bool, len(container.Files))
	for i := range container.Packages {
		if err := container.Walk(i, func(path string, index int) error {
			code[index] = code[index] || IsCodeModule(path)
			return nil
		}); err != nil {
			return check, err
		}
	}

	// the pieces of data to read, each once, in the order they are stored
	type key struct {
		offset, stored, size uint64
		codec                byte
	}
	byKey := map[key]*nupExtent{}
	empty := sha256.Sum256(nil)
	reasons := make([]string, len(container.Files))
	for i, f := range container.Files {
		if f.Size == 0 {
			if f.Hash != empty {
				reasons[i] = "its SHA-256 does not match"
			}
			continue
		}
		k := key{f.ExtentOffset, f.ExtentSize, f.Size, f.Codec}
		job, ok := byKey[k]
		if !ok {
			job = &nupExtent{offset: f.ExtentOffset, stored: f.ExtentSize, size: f.Size, codec: f.Codec}
			byKey[k] = job
		}
		job.files = append(job.files, i)
		job.code = job.code || code[i]
	}
	jobs := make([]*nupExtent, 0, len(byKey))
	total := int64(0)
	for _, job := range byKey {
		jobs = append(jobs, job)
		total += int64(job.stored)
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].offset != jobs[j].offset {
			return jobs[i].offset < jobs[j].offset
		}
		return jobs[i].stored < jobs[j].stored
	})
	if progress != nil {
		progress(0, total)
	}
	workers := threadsOf(ctx)
	if workers <= 0 {
		workers = UsableProcessors()
	}
	if err := readNupExtents(ctx, file, jobs, workers, func(done int64) {
		if progress != nil {
			progress(done, total)
		}
	}); err != nil {
		return check, err
	}
	// a module whose data is not what was packed is damaged by its data, not by the dump
	codeReasons := make([]string, len(container.Files))
	for _, job := range jobs {
		for _, index := range job.files {
			switch {
			case job.reason != "":
				reasons[index] = job.reason
			case container.Files[index].Hash != job.sum:
				reasons[index] = "its SHA-256 does not match"
			case code[index]:
				codeReasons[index] = job.codeProblem
			}
		}
	}

	// the damaged files by their path, found in the tree of each package
	damaged := []NupDamaged{}
	placed := make([]bool, len(container.Files))
	for i, pkg := range container.Packages {
		label := nupLabel(pkg)
		err := container.Walk(i, func(path string, index int) error {
			switch {
			case placed[index]:
			case reasons[index] != "":
				damaged = append(damaged, NupDamaged{Package: label, Path: path, Reason: reasons[index]})
			case codeReasons[index] != "":
				damaged = append(damaged, NupDamaged{Package: label, Path: path, Reason: codeReasons[index], Code: true})
			}
			placed[index] = true
			return nil
		})
		if err != nil {
			return check, err
		}
	}
	for index, reason := range reasons {
		if !placed[index] && reason != "" {
			damaged = append(damaged, NupDamaged{Path: container.Files[index].Name, Reason: reason})
		}
	}
	damaged = append(damaged, checkNupMeta(container)...)
	if len(damaged) > 0 {
		return check, &NupDamagedError{Damaged: damaged}
	}
	return check, nil
}

// readNupExtents reads the pieces of data in their order and hands each to the first worker
// free. The first error of a read, or a cancelled ctx, stops it.
func readNupExtents(ctx context.Context, file *os.File, jobs []*nupExtent, workers int, progress func(done int64)) error {
	// the max of this package compares bytes
	workers = min(workers, len(jobs))
	if workers < 1 {
		workers = 1
	}
	free := make(chan []byte, workers*(nupQueued+1)+2)
	for range cap(free) {
		free <- make([]byte, nupChunkSize)
	}
	idle := make(chan int, workers)
	feeds := make([]chan *nupFeed, workers)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	for w := range workers {
		feeds[w] = make(chan *nupFeed, 1)
		idle <- w
		wait.Add(1)
		go func() {
			defer wait.Done()
			worker := nupHasher{free: free}
			defer worker.close()
			for f := range feeds[w] {
				worker.check(f)
				idle <- w
			}
		}()
	}

	var done atomic.Int64
	err := readNupStretch(ctx, file, jobs, free, idle, feeds, func(n int64) {
		progress(done.Add(n))
	})
	for _, f := range feeds {
		close(f)
	}
	wait.Wait()
	return err
}

// readNupStretch reads the pieces in their order, small pieces in a few big reads, and hands each
// to the first worker free.
func readNupStretch(ctx context.Context, file *os.File, jobs []*nupExtent, free chan []byte, idle chan int, feeds []chan *nupFeed,
	tell func(int64)) error {
	var window []byte
	windowAt := int64(-1)
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		f := &nupFeed{job: job, chunks: make(chan []byte, nupQueued)}
		var w int
		select {
		case w = <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
		feeds[w] <- f
		at, end := int64(job.offset), int64(job.offset+job.stored)
		for at < end {
			if err := ctx.Err(); err != nil {
				close(f.chunks)
				return err
			}
			buffer := <-free
			n := min(int64(len(buffer)), end-at)
			var err error
			if job.stored < nupWindowSize/2 {
				// small pieces come from the window, read again when the piece goes past it
				if windowAt < 0 || at < windowAt || end > windowAt+int64(len(window)) {
					if window == nil {
						window = make([]byte, nupWindowSize)
					}
					window = window[:cap(window)]
					var got int
					got, err = file.ReadAt(window, at)
					if errors.Is(err, io.EOF) && int64(got) >= end-at {
						err = nil
					}
					window, windowAt = window[:got], at
				}
				if err == nil {
					copy(buffer, window[at-windowAt:at-windowAt+n])
				}
			} else {
				var got int
				got, err = file.ReadAt(buffer[:n], at)
				if errors.Is(err, io.EOF) && int64(got) == n {
					err = nil
				}
			}
			if err != nil {
				free <- buffer
				close(f.chunks)
				return err
			}
			f.chunks <- buffer[:n]
			at += n
			tell(n)
		}
		close(f.chunks)
	}
	return nil
}

// nupHasher is a worker: it hashes the data of a piece as it comes, decompressed for zstd.
type nupHasher struct {
	free    chan []byte
	decoder *zstd.Decoder
	out     []byte
}

func (h *nupHasher) close() {
	if h.decoder != nil {
		h.decoder.Close()
	}
}

// check hashes the data of a piece, and keeps in the piece its hash or why it is damaged. Its
// chunks are all taken, whatever happens, so the reader never waits for it.
func (h *nupHasher) check(f *nupFeed) {
	job := f.job
	hash := sha256.New()
	source := &nupChunkReader{chunks: f.chunks, free: h.free}
	defer source.drain()
	var (
		n   int64
		err error
		// a code module is read by its header as it is hashed
		into   io.Writer = hash
		module *nsoCheck
	)
	if job.code {
		module = newNsoCheck(int64(job.size))
		into = io.MultiWriter(hash, module)
	}
	if job.codec == nupCodecZstd {
		if h.decoder == nil {
			if h.decoder, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(nupMaxWindow),
				zstd.WithDecoderMaxMemory(nupMaxWindow)); err != nil {
				job.reason = fmt.Sprintf("its zstd data cannot be decompressed (%v)", err)
				return
			}
			h.out = make([]byte, nupChunkSize)
		}
		if err = h.decoder.Reset(source); err == nil {
			// one byte more than the file has tells data that goes on past it
			n, err = io.CopyBuffer(into, io.LimitReader(h.decoder, int64(job.size)+1), h.out)
		}
	} else {
		n, err = source.hashAll(into)
	}
	source.drain()
	switch {
	case err != nil:
		job.reason = fmt.Sprintf("its zstd data cannot be decompressed (%v)", err)
	case n > int64(job.size):
		job.reason = fmt.Sprintf("its data decompresses to more than its %d bytes", job.size)
	case n < int64(job.size):
		job.reason = fmt.Sprintf("its data decompresses to %d bytes instead of %d", n, job.size)
	default:
		copy(job.sum[:], hash.Sum(nil))
		if module != nil {
			job.codeProblem = module.result()
		}
	}
}

// nupChunkReader reads the chunks of a piece as one stream, giving each buffer back once read.
type nupChunkReader struct {
	chunks <-chan []byte
	free   chan<- []byte
	held   []byte
	rest   []byte
}

func (r *nupChunkReader) Read(p []byte) (int, error) {
	for len(r.rest) == 0 {
		r.release()
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		r.held, r.rest = chunk, chunk
	}
	n := copy(p, r.rest)
	r.rest = r.rest[n:]
	return n, nil
}

// hashAll writes every chunk into to as it is, without copying it, and gives the bytes.
func (r *nupChunkReader) hashAll(to io.Writer) (int64, error) {
	n := int64(0)
	for chunk := range r.chunks {
		_, err := to.Write(chunk)
		n += int64(len(chunk))
		r.free <- chunk[:cap(chunk)]
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func (r *nupChunkReader) release() {
	if r.held != nil {
		r.free <- r.held[:cap(r.held)]
		r.held, r.rest = nil, nil
	}
}

// drain takes the chunks left, when the data stopped being read before its end.
func (r *nupChunkReader) drain() {
	r.release()
	for chunk := range r.chunks {
		r.free <- chunk[:cap(chunk)]
	}
}
