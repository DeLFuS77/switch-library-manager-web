package switchfs

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

// A complete pack puts a game, its update and its DLC into one NSP (or NSZ): the files of
// each package (NCAs, tickets and certificates) are copied one after the other into a new
// PFS0, as multi-content installers expect it. Nothing is decrypted or changed: the pack has
// the same files as the packages it is made of, and installs the same way.

// packSource is a package of a pack, opened, with its files.
type packSource struct {
	path   string
	reader ReadAtCloser
	pkg    *gamePackage
}

// PackHasCompressedFiles reports whether a pack of these packages holds compressed files
// (NCZ), so it is an NSZ.
func PackHasCompressedFiles(sources []string) (bool, error) {
	for _, source := range sources {
		reader, err := OpenFile(source)
		if err != nil {
			return false, err
		}
		pkg, err := openGamePackage(reader)
		reader.Close()
		if err != nil {
			return false, err
		}
		for _, file := range pkg.files {
			if strings.HasSuffix(strings.ToLower(file.name), ".ncz") {
				return true, nil
			}
		}
	}
	return false, nil
}

// deltaFragmentNames gives the names of the files of a package that are delta fragments, as
// its CNMTs list them. Without the keys to read the CNMTs, it gives none: everything is kept.
func deltaFragmentNames(reader io.ReaderAt, pkg *gamePackage) map[string]bool {
	ids := map[string]bool{}
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".cnmt.nca") {
			continue
		}
		_, section, err := openMetaNcaDataSection(reader, file.offset)
		if err != nil {
			continue
		}
		pfs0, err := readPfs0(bytes.NewReader(section), 0x0)
		if err != nil {
			continue
		}
		for _, id := range cnmtDeltaFragments(pfs0, section) {
			ids[id] = true
		}
	}
	names := map[string]bool{}
	for _, file := range pkg.files {
		lower := strings.ToLower(file.name)
		id := strings.TrimSuffix(strings.TrimSuffix(lower, ".nca"), ".ncz")
		if id != lower && ids[id] {
			names[file.name] = true
		}
	}
	return names
}

// MergePackages writes the files of the packages into one NSP or NSZ at target, in their
// order, each file once (the same certificate is in several packages), and without the
// delta fragments of the updates when withoutDeltas is set. The target is removed if the
// pack cannot be made. It gives how many bytes of delta fragments were left out.
func MergePackages(ctx context.Context, sources []string, target string, withoutDeltas bool, progress func(done int64, total int64)) (int64, error) {
	skipped, _, err := mergePackages(ctx, sources, target, withoutDeltas, progress)
	return skipped, err
}

// MergeAndCheck is MergePackages that checks the pack while writing it, instead of reading it
// again (see DecompressAndCheck); its NCZ files are still decompressed from the disk to be
// checked. On an error the pack is removed.
func MergeAndCheck(ctx context.Context, sources []string, target string, withoutDeltas bool, progress func(done int64, total int64)) (int64, FileCheck, error) {
	skipped, written, err := mergePackages(ctx, sources, target, withoutDeltas, progress)
	if err != nil {
		return 0, FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
		return 0, check, err
	}
	return skipped, check, nil
}

func mergePackages(ctx context.Context, sources []string, target string, withoutDeltas bool, progress func(done int64, total int64)) (int64, []*writtenFile, error) {
	if len(sources) < 2 {
		return 0, nil, errors.New("a pack needs at least two packages")
	}
	opened := []packSource{}
	defer func() {
		for _, source := range opened {
			source.reader.Close()
		}
	}()
	names := []string{}
	type part struct {
		source int
		file   packedFile
	}
	parts := []part{}
	seen := map[string]bool{}
	var total, skipped int64
	for _, path := range sources {
		reader, err := OpenFile(path)
		if err != nil {
			return 0, nil, err
		}
		pkg, err := openGamePackage(reader)
		if err != nil {
			reader.Close()
			return 0, nil, err
		}
		opened = append(opened, packSource{path: path, reader: reader, pkg: pkg})
		deltas := map[string]bool{}
		if withoutDeltas {
			deltas = deltaFragmentNames(reader, pkg)
		}
		for _, file := range pkg.files {
			// the same content once, also as an NCA in one package and an NCZ in another; a
			// ticket and its certificate share their rights ID, and an NCA and its XML their ID,
			// but they are different files
			key := strings.ToLower(file.name)
			if id := contentId(file.name); id != "" && (strings.HasSuffix(key, ".nca") || strings.HasSuffix(key, ".ncz")) {
				key = id
			}
			if seen[key] {
				continue
			}
			if deltas[file.name] {
				skipped += file.size
				continue
			}
			seen[key] = true
			names = append(names, file.name)
			parts = append(parts, part{source: len(opened) - 1, file: file})
			total += file.size
		}
	}
	if len(parts) == 0 {
		return 0, nil, errors.New("the packages are empty")
	}

	var done int64
	report := func(n int64) {
		done += n
		if progress != nil {
			progress(done, total)
		}
	}
	output, err := CreateNew(target)
	if err != nil {
		return 0, nil, err
	}
	writer := &pfs0Writer{file: output, names: names}
	written := make([]*writtenFile, len(parts))
	err = writer.begin()
	for i, p := range parts {
		if err != nil {
			break
		}
		writer.startFile()
		written[i] = newWrittenFile(names[i])
		err = copyRange(ctx, io.MultiWriter(writer.file, written[i]), opened[p.source].reader, p.file.offset, p.file.size, report)
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
		return 0, nil, err
	}
	return skipped, written, nil
}
