package switchfs

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// RecompressGame writes an NSZ or an XCZ again, solid or in blocks as options tells (an XCZ
// always in blocks), at the level of options: the decrypted data its NCZ files hold is
// decompressed and compressed again, with their header and section table as they are. No key
// is needed and no NSP is written in between. It does not verify the result, see VerifyFile.
func RecompressGame(ctx context.Context, source string, target string, options CompressOptions) (CompressResult, error) {
	result := CompressResult{}
	input, err := os.Open(source)
	if err != nil {
		return result, err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return result, err
	}
	result.InputSize = info.Size()
	pkg, err := openGamePackage(input)
	if err != nil {
		return result, err
	}
	names := make([]string, len(pkg.files))
	for i, file := range pkg.files {
		names[i] = file.name
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			result.Compressed++
		}
	}
	if result.Compressed == 0 {
		return result, errors.New("it holds no compressed file: compress it instead")
	}

	output, err := CreateNew(target)
	if err != nil {
		return result, err
	}
	var done int64
	report := func(n int64) {
		done += n
		if options.Progress != nil {
			options.Progress(done, result.InputSize)
		}
	}
	writer := newPackageWriter(output, input, pkg, names)
	err = writer.begin()
	for _, file := range pkg.files {
		if err != nil {
			break
		}
		if err = ctx.Err(); err != nil {
			break
		}
		writer.startFile()
		// the progress follows the bytes of the source read
		counter := &countingReaderAt{reader: input, start: file.offset, end: file.offset + file.size, report: report}
		if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
			err = recompressNcz(ctx, output, counter, file.offset, file.size, options.Blocks || pkg.xci, options)
			if rest := file.size - counter.counted; err == nil && rest > 0 {
				report(rest)
			}
		} else {
			result.Stored++
			err = copyRange(ctx, output, input, file.offset, file.size, report)
		}
		if err == nil {
			err = writer.endFile()
		}
	}
	if err == nil {
		err = writer.finish()
	}
	closeErr := output.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return result, err
	}
	if info, statErr := os.Stat(target); statErr == nil {
		result.OutputSize = info.Size()
	}
	if options.Progress != nil && done < result.InputSize {
		options.Progress(result.InputSize, result.InputSize)
	}
	return result, nil
}

// recompressNcz writes the NCZ at offset in input again: its header and section table as they
// are, then its data as one stream, or in blocks.
func recompressNcz(ctx context.Context, output *os.File, input io.ReaderAt, offset int64, size int64, blocks bool, options CompressOptions) error {
	reader := io.NewSectionReader(input, offset, size)
	head := make([]byte, nczHeaderSize+0x10)
	if _, err := io.ReadFull(reader, head); err != nil {
		return err
	}
	count := binary.LittleEndian.Uint64(head[nczHeaderSize+8:])
	if string(head[nczHeaderSize:nczHeaderSize+8]) != nczSectionMagic || count == 0 || count > nczMaxSections || count > uint64(size)/0x40 {
		return errors.New("invalid NCZ section table")
	}
	table := make([]byte, count*0x40)
	if _, err := io.ReadFull(reader, table); err != nil {
		return err
	}
	// the data runs from the end of the copied header to the end of the last section
	last := table[(count-1)*0x40:]
	end := binary.LittleEndian.Uint64(last[0:8]) + binary.LittleEndian.Uint64(last[8:16])
	if end <= nczHeaderSize {
		return errors.New("invalid NCZ section table")
	}
	dataSize := end - nczHeaderSize
	if _, err := output.Write(head); err != nil {
		return err
	}
	if _, err := output.Write(table); err != nil {
		return err
	}

	position, _ := reader.Seek(0, io.SeekCurrent)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(reader, magic); err != nil {
		return err
	}
	var stream io.Reader
	if string(magic) == nczBlockMagic {
		blockReader, err := newNczBlockReader(reader)
		if err != nil {
			return err
		}
		defer blockReader.Close()
		stream = blockReader
	} else {
		reader.Seek(position, io.SeekStart)
		decoder, err := zstd.NewReader(bufio.NewReaderSize(reader, nczChunkSize), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(1<<30), zstd.WithDecoderLowmem(false))
		if err != nil {
			return err
		}
		defer decoder.Close()
		stream = decoder
	}

	if blocks {
		fill := func(data []byte) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := io.ReadFull(stream, data); err != nil {
				return errors.New("NCZ data ends early")
			}
			return nil
		}
		return writeNczBlockData(ctx, output, dataSize, fill, options, func(int64) {})
	}
	encoder, err := newStreamEncoder(output, options.Level, options.Workers)
	if err != nil {
		return err
	}
	buffer := make([]byte, nczChunkSize)
	for left := dataSize; left > 0; {
		if err := ctx.Err(); err != nil {
			encoder.Close()
			return err
		}
		chunk := buffer[:min(uint64(len(buffer)), left)]
		if _, err := io.ReadFull(stream, chunk); err != nil {
			encoder.Close()
			return errors.New("NCZ data ends early")
		}
		if _, err := encoder.Write(chunk); err != nil {
			encoder.Close()
			return err
		}
		left -= uint64(len(chunk))
	}
	return encoder.Close()
}
