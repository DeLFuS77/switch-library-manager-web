package switchfs

import (
	"context"
	"os"
	"sync"
	"time"
)

// Estimate is what a compression of a game would give, from samples of it.
type Estimate struct {
	InputSize int64
	// the size of the NSZ or XCZ, and the time the compression and its check would take on
	// this computer
	OutputSize int64
	Duration   time.Duration
	// the bytes compressed to tell it, of the ones that would be
	Sampled      int64
	Compressible int64
}

// byteCounter counts what is written to it.
type byteCounter struct{ n int64 }

func (c *byteCounter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// EstimateCompression tells the size and the time of a compression of a game with options,
// without writing it: samples of sampleSize bytes, spread over the NCA files that would be
// compressed, are compressed as a compression would; the rest of the game is counted as it is.
// A game smaller than the samples is compressed whole, and the estimate is then its size.
func EstimateCompression(ctx context.Context, source string, options CompressOptions, samples int, sampleSize int64) (Estimate, error) {
	estimate := Estimate{}
	input, err := os.Open(source)
	if err != nil {
		return estimate, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return estimate, err
	}
	estimate.InputSize = info.Size()
	pkg, err := openGamePackage(input)
	if err != nil {
		return estimate, err
	}
	titleKeys, err := readTickets(input, pkg.files)
	if err != nil {
		return estimate, err
	}

	type part struct {
		file *packedFile
		plan *nczPlan
	}
	parts := []part{}
	for i, file := range pkg.files {
		if !isCompressibleNcaName(file.name) || file.size <= nczHeaderSize {
			continue
		}
		plan, _, err := planNcz(input, file.offset, uint64(file.size), titleKeys)
		if err != nil {
			return estimate, err
		}
		if plan != nil {
			parts = append(parts, part{file: &pkg.files[i], plan: plan})
			estimate.Compressible += file.size - nczHeaderSize
		}
	}
	if len(parts) == 0 {
		return estimate, ErrNothingToMeasure
	}
	if samples < 1 {
		samples = 1
	}
	if sampleSize < 1<<16 {
		sampleSize = 1 << 16
	}

	// the samples, spread evenly over the data of the parts one after the other
	type sample struct {
		part     part
		position uint64
		size     uint64
	}
	chosen := []sample{}
	if estimate.Compressible <= int64(samples)*sampleSize {
		for _, p := range parts {
			chosen = append(chosen, sample{part: p, position: nczHeaderSize, size: uint64(p.file.size - nczHeaderSize)})
		}
	} else {
		for k := 0; k < samples; k++ {
			at := int64(float64(estimate.Compressible) * (float64(k) + 0.5) / float64(samples))
			for _, p := range parts {
				data := p.file.size - nczHeaderSize
				if at >= data {
					at -= data
					continue
				}
				// centred on its place, inside the part (switchfs has a max of its own, for bytes)
				start := min(at-sampleSize/2, data-sampleSize)
				if start < 0 {
					start = 0
				}
				size := min(sampleSize, data)
				chosen = append(chosen, sample{part: p, position: nczHeaderSize + uint64(start)&^15, size: uint64(size)})
				break
			}
		}
	}

	// the speed the file is read at: a compression reads it whole, and its check again
	readSpeed, err := measureRead(ctx, input, estimate.InputSize, estimateReadSize)
	if err != nil {
		return estimate, err
	}

	// the samples are compressed at once, one by thread, as a compression keeps every thread
	// busy; each with the encoder a compression of that kind uses
	workers := options.Workers
	if workers < 1 {
		workers = 1
	}
	var (
		mutex                       sync.Mutex
		plainBytes, compressedBytes int64
		firstErr                    error
		wait                        sync.WaitGroup
	)
	var total int64
	for _, s := range chosen {
		total += int64(s.size)
	}
	compressSample := func(s sample) error {
		counter := &byteCounter{}
		var write func([]byte) error
		var finish func() error
		if options.Blocks || pkg.xci {
			encoder, err := newBlockEncoder(options.Level, 1)
			if err != nil {
				return err
			}
			var out []byte
			write = func(chunk []byte) error {
				for len(chunk) > 0 {
					block := chunk[:min(len(chunk), 1<<nczBlockExponent)]
					if out, err = encoder.encode(0, out[:0], block); err != nil {
						return err
					}
					counter.n += int64(min(len(out), len(block)))
					chunk = chunk[len(block):]
				}
				return nil
			}
			finish = encoder.Close
		} else {
			encoder, err := newStreamEncoder(counter, options.Level, 1)
			if err != nil {
				return err
			}
			write = func(chunk []byte) error {
				_, err := encoder.Write(chunk)
				return err
			}
			finish = encoder.Close
		}
		plain := nczPlainReader{input: input, offset: s.part.file.offset, sections: s.part.plan.sections}
		buffer := make([]byte, 1<<nczBlockExponent)
		for position, end := s.position, s.position+s.size; position < end; {
			if err := ctx.Err(); err != nil {
				finish()
				return err
			}
			chunk := buffer[:min(uint64(len(buffer)), end-position)]
			if err := plain.read(chunk, position); err != nil {
				finish()
				return err
			}
			if err := write(chunk); err != nil {
				finish()
				return err
			}
			position += uint64(len(chunk))
			mutex.Lock()
			plainBytes += int64(len(chunk))
			if options.Progress != nil {
				options.Progress(plainBytes, total)
			}
			mutex.Unlock()
		}
		if err := finish(); err != nil {
			return err
		}
		mutex.Lock()
		compressedBytes += counter.n
		mutex.Unlock()
		return nil
	}
	queue := make(chan sample)
	started := time.Now()
	for w := 0; w < min(workers, len(chosen)); w++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for s := range queue {
				if err := compressSample(s); err != nil {
					mutex.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mutex.Unlock()
				}
			}
		}()
	}
	for _, s := range chosen {
		queue <- s
	}
	close(queue)
	wait.Wait()
	spent := time.Since(started)
	if firstErr != nil {
		return estimate, firstErr
	}
	// with fewer samples than threads, the threads left would have had work of their own
	if busy := min(workers, len(chosen)); busy < workers {
		spent = time.Duration(float64(spent) * float64(busy) / float64(workers))
	}
	estimate.Sampled = plainBytes
	ratio := float64(compressedBytes) / float64(plainBytes)
	estimate.OutputSize = estimate.InputSize - estimate.Compressible + int64(float64(estimate.Compressible)*ratio)
	// compressing and reading go on together, the slower one sets the pace; the check reads the
	// new file whole and decompresses it, as fast as the original was read
	compressing := time.Duration(float64(spent) * float64(estimate.Compressible) / float64(plainBytes))
	reading := time.Duration(float64(estimate.InputSize) / readSpeed * float64(time.Second))
	estimate.Duration = compressing + reading
	if reading > compressing {
		estimate.Duration = 2 * reading
	}
	return estimate, nil
}

// how much of a file is read to tell its read speed
const estimateReadSize = 256 << 20

// measureRead gives the bytes per second a file is read at, from a part of it in its middle.
func measureRead(ctx context.Context, input *os.File, size int64, length int64) (float64, error) {
	length = min(length, size)
	start := (size - length) / 2 &^ 0xFFF
	buffer := make([]byte, nczChunkSize)
	began := time.Now()
	for done := int64(0); done < length; {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		chunk := buffer[:min(int64(len(buffer)), length-done)]
		if err := readAtFull(input, chunk, start+done); err != nil {
			return 0, err
		}
		done += int64(len(chunk))
	}
	seconds := time.Since(began).Seconds()
	if seconds <= 0 {
		seconds = 1e-6
	}
	return float64(length) / seconds, nil
}
