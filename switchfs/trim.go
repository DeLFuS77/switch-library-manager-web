package switchfs

import (
	"context"
	"errors"
	"io"
	"os"
)

// ErrNothingToTrim is returned for an XCI that ends where its data ends.
var ErrNothingToTrim = errors.New("it is already trimmed")

// XciDataEnd is where the data of an XCI ends: the end of the last partition of its root
// HFS0, aligned to 0x200. What follows in a dump of a game card is padding.
func XciDataEnd(input io.ReaderAt) (int64, error) {
	pkg, err := openGamePackage(input)
	if err != nil {
		return 0, err
	}
	if !pkg.xci {
		return 0, errors.New("only XCI files can be trimmed")
	}
	root, err := readPfs0(input, pkg.rootOffset)
	if err != nil {
		return 0, err
	}
	// the offsets of the partitions are read with the length of the header already added
	end := pkg.rootOffset + int64(root.HeaderLen)
	for _, partition := range root.Files {
		if partitionEnd := pkg.rootOffset + int64(partition.StartOffset) + int64(partition.Size); partitionEnd > end {
			end = partitionEnd
		}
	}
	return (end + 0x1FF) &^ 0x1FF, nil
}

// checkPadding tells whether input holds only padding from start to end: 0xFF as game cards
// are dumped, or 0x00.
func checkPadding(ctx context.Context, input io.ReaderAt, start int64, end int64, progress func(int64)) error {
	buffer := make([]byte, nczChunkSize)
	var fill byte
	for position := start; position < end; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := buffer[:min(int64(len(buffer)), end-position)]
		if err := readAtFull(input, chunk, position); err != nil {
			return err
		}
		if position == start {
			fill = chunk[0]
			if fill != 0xFF && fill != 0x00 {
				return errors.New("data follows the last partition: it is not padding, the XCI is left as it is")
			}
		}
		for _, value := range chunk {
			if value != fill {
				return errors.New("data follows the last partition: it is not padding, the XCI is left as it is")
			}
		}
		if progress != nil {
			progress(int64(len(chunk)))
		}
		position += int64(len(chunk))
	}
	return nil
}

// TrimXci writes an XCI without the padding after its data to target, or, when target is
// source, cuts it in place. The padding is checked first: an XCI with anything else after
// its last partition is left as it is. It gives the bytes taken away.
func TrimXci(ctx context.Context, source string, target string, progress func(done int64, total int64)) (int64, error) {
	input, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	info, err := input.Stat()
	if err != nil {
		input.Close()
		return 0, err
	}
	size := info.Size()
	end, err := XciDataEnd(input)
	if err != nil {
		input.Close()
		return 0, err
	}
	if end >= size {
		input.Close()
		return 0, ErrNothingToTrim
	}
	inPlace := sameFile(source, target)
	// in place only the padding is read; a copy reads the data as well
	total := size - end
	if !inPlace {
		total = size
	}
	var done int64
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	if err := checkPadding(ctx, input, end, size, report); err != nil {
		input.Close()
		return 0, err
	}
	if inPlace {
		input.Close()
		if err := os.Truncate(source, end); err != nil {
			return 0, err
		}
		return size - end, nil
	}
	defer input.Close()
	output, err := CreateNew(target)
	if err != nil {
		return 0, err
	}
	err = copyRange(ctx, output, input, 0, end, report)
	if err == nil {
		err = output.Sync()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return 0, err
	}
	return size - end, nil
}

// sameFile tells whether two paths name the same file.
func sameFile(a string, b string) bool {
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && os.SameFile(first, second)
}
