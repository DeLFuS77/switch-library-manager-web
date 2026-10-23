package switchfs

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

// ErrNothingToMeasure is returned when a game holds no NCA that would be compressed.
var ErrNothingToMeasure = errors.New("no part of this game is compressed")

// MeasureCompression compresses the biggest NCA of a game to output as a compression
// would, from the share from (0 to 1) of it on and for about duration, and gives the bytes
// of the game it compressed: what reading, decrypting, compressing and writing do together
// on this computer. The zstd library in C gives each of its threads a piece of several MiB
// of one stream: the measure stops only once every thread had whole pieces, else the threads
// left without one at the end would count as slow; the NCA is read again from its start if
// it is too small for that.
func MeasureCompression(ctx context.Context, source string, from float64, options CompressOptions, duration time.Duration, output io.Writer) (int64, error) {
	input, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return 0, err
	}
	titleKeys, err := readTickets(input, pkg.files)
	if err != nil {
		return 0, err
	}
	var biggest *packedFile
	for i, file := range pkg.files {
		if isCompressibleNcaName(file.name) && file.size > nczHeaderSize && (biggest == nil || file.size > biggest.size) {
			biggest = &pkg.files[i]
		}
	}
	if biggest == nil {
		return 0, ErrNothingToMeasure
	}
	plan, _, err := planNcz(input, biggest.offset, uint64(biggest.size), titleKeys)
	if err != nil {
		return 0, err
	}
	if plan == nil {
		return 0, ErrNothingToMeasure
	}

	// the encoder a compression uses, so the measure tells the speed it will have
	var encoder io.WriteCloser
	if options.Blocks {
		encoder, err = newMeasureBlockWriter(output, options.Level, options.Workers)
	} else {
		encoder, err = newStreamEncoder(output, options.Level, options.Workers)
	}
	if err != nil {
		return 0, err
	}
	defer encoder.Close()

	plain := nczPlainReader{input: input, offset: biggest.offset, sections: plan.sections}
	dataSize := uint64(biggest.size) - nczHeaderSize
	if from < 0 || from >= 1 {
		from = 0
	}
	position := nczHeaderSize + uint64(float64(dataSize)*from)&^(nczChunkSize-1)
	// with blocks, a block for each worker at once, as a compression in blocks reads them
	buffer := make([]byte, nczChunkSize)
	var round uint64
	if !options.Blocks && options.Workers > 1 {
		round = uint64(nativeJobSize(options.Level)) * uint64(options.Workers)
	}
	if options.Blocks {
		workers := options.Workers
		if workers < 1 {
			workers = 1
		}
		buffer = make([]byte, workers<<nczBlockExponent)
	}
	var done int64
	start := time.Now()
	for time.Since(start) < duration || (round > 0 && uint64(done)%round != 0) {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		if position >= uint64(biggest.size) {
			if round == 0 {
				break
			}
			position = nczHeaderSize
		}
		chunk := buffer[:min(uint64(len(buffer)), uint64(biggest.size)-position)]
		if round > 0 {
			chunk = chunk[:min(uint64(len(chunk)), round-uint64(done)%round)]
		}
		if err := plain.read(chunk, position); err != nil {
			return done, err
		}
		if _, err := encoder.Write(chunk); err != nil {
			return done, err
		}
		done += int64(len(chunk))
		position += uint64(len(chunk))
	}
	return done, encoder.Close()
}

// measureBlockWriter compresses what is written to it in blocks, a block per worker at once,
// as writeNczBlockData does, and writes the blocks in order: the speed of a compression in
// blocks, without the table of their sizes.
type measureBlockWriter struct {
	output  io.Writer
	encoder blockEncoder
	outs    [][]byte
	errs    []error
}

func newMeasureBlockWriter(output io.Writer, level string, workers int) (*measureBlockWriter, error) {
	if workers < 1 {
		workers = 1
	}
	encoder, err := newBlockEncoder(level, workers)
	if err != nil {
		return nil, err
	}
	return &measureBlockWriter{output: output, encoder: encoder, outs: make([][]byte, workers), errs: make([]error, workers)}, nil
}

func (w *measureBlockWriter) Write(p []byte) (int, error) {
	const blockSize = 1 << nczBlockExponent
	for done := 0; done < len(p); {
		var wait sync.WaitGroup
		n := 0
		for ; n < len(w.outs) && done+n*blockSize < len(p); n++ {
			block := p[done+n*blockSize : min(done+(n+1)*blockSize, len(p))]
			wait.Add(1)
			go func(worker int) {
				defer wait.Done()
				w.outs[worker], w.errs[worker] = w.encoder.encode(worker, w.outs[worker][:0], block)
			}(n)
		}
		wait.Wait()
		for i := 0; i < n; i++ {
			if w.errs[i] != nil {
				return done, w.errs[i]
			}
			if _, err := w.output.Write(w.outs[i]); err != nil {
				return done, err
			}
		}
		done = min(len(p), done+n*blockSize)
	}
	return len(p), nil
}

func (w *measureBlockWriter) Close() error {
	return w.encoder.Close()
}
