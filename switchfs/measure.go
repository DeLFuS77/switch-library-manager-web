package switchfs

import (
	"context"
	"errors"
	"io"
	"os"
	"time"
)

// ErrNothingToMeasure is returned when a game holds no NCA that would be compressed.
var ErrNothingToMeasure = errors.New("no part of this game is compressed")

// MeasureCompression compresses the biggest NCA of a game to output as a compression
// would, from the share from (0 to 1) of it on and for at most duration, and gives the bytes
// of the game it compressed: what reading, decrypting, compressing and writing do together
// on this computer.
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
	encoder, err := newStreamEncoder(output, options.Level, options.Workers)
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
	buffer := make([]byte, nczChunkSize)
	var done int64
	start := time.Now()
	for position < uint64(biggest.size) && time.Since(start) < duration {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		chunk := buffer[:min(uint64(len(buffer)), uint64(biggest.size)-position)]
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
