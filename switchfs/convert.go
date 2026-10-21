package switchfs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// An XCI (a game card) keeps the files of the game in its secure partition, the same files
// an NSP holds: converting it copies them into an NSP. An XCZ becomes an NSZ the same way,
// with its compressed files as they are. Game cards need no ticket, so installers take the
// NSP as it is.

// ConvertedPath is the name of the NSP of an XCI (.nsp), or the NSZ of an XCZ (.nsz).
func ConvertedPath(path string) string {
	extension := filepath.Ext(path)
	switch strings.ToLower(extension) {
	case ".xcz":
		return strings.TrimSuffix(path, extension) + ".nsz"
	default:
		return strings.TrimSuffix(path, extension) + ".nsp"
	}
}

// ConvertXciToNsp writes the files of the secure partition of an XCI or XCZ to an NSP or
// NSZ at target; the target is removed if the conversion fails.
func ConvertXciToNsp(ctx context.Context, source string, target string, progress func(done int64, total int64)) error {
	_, err := convertXciToNsp(ctx, source, target, progress)
	return err
}

// ConvertAndCheck is ConvertXciToNsp that checks the NSP while writing it, instead of reading
// it again (see DecompressAndCheck). On an error the NSP is removed.
func ConvertAndCheck(ctx context.Context, source string, target string, progress func(done int64, total int64)) (FileCheck, error) {
	written, err := convertXciToNsp(ctx, source, target, progress)
	if err != nil {
		return FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return check, err
}

func convertXciToNsp(ctx context.Context, source string, target string, progress func(done int64, total int64)) ([]*writtenFile, error) {
	input, err := os.Open(source)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	pkg, err := openGamePackage(input)
	if err != nil {
		return nil, err
	}
	if !pkg.xci {
		return nil, errors.New("not an XCI or XCZ file")
	}
	if len(pkg.files) == 0 {
		return nil, errors.New("the secure partition of the game card is empty")
	}

	names := make([]string, len(pkg.files))
	var total, done int64
	for i, file := range pkg.files {
		names[i] = file.name
		total += file.size
	}
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}

	output, err := CreateNew(target)
	if err != nil {
		return nil, err
	}
	// a header just big enough for the names, as in the NSP files of the eShop
	writer := &pfs0Writer{file: output, names: names}
	written := make([]*writtenFile, len(pkg.files))
	err = writer.begin()
	for i, file := range pkg.files {
		if err != nil {
			break
		}
		writer.startFile()
		written[i] = newWrittenFile(names[i])
		err = copyRange(ctx, io.MultiWriter(writer.file, written[i]), input, file.offset, file.size, report)
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
		return nil, err
	}
	return written, nil
}
