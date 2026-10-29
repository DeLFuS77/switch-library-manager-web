package switchfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// DeltaFragmentsSize gives how many bytes the delta fragments of an update take in a file, as
// its CNMTs list them: what a pack, or MergeAndCheck of the file alone, leaves out.
func DeltaFragmentsSize(path string) (int64, error) {
	reader, err := OpenFile(path)
	if err != nil {
		return 0, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return 0, err
	}
	if pkg.xci {
		return 0, nil
	}
	deltas := deltaFragmentNames(reader, pkg)
	var size int64
	for _, file := range pkg.files {
		if deltas[file.name] {
			size += file.size
		}
	}
	return size, nil
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
	return writePackages(ctx, sources, target, withoutDeltas, nil, progress)
}

// MergeNewestAndCheck is MergeAndCheck that keeps, of a title found in several packages, its
// highest version only: a SuperNSP merged with a newer update of its game gets the new update
// in place of the old one. It gives the bytes of delta fragments left out and the titles
// replaced ("<title ID> v<version>").
func MergeNewestAndCheck(ctx context.Context, sources []string, target string, withoutDeltas bool, progress func(done int64, total int64)) (int64, []string, FileCheck, error) {
	if len(sources) < 2 {
		return 0, nil, FileCheck{}, errors.New("a pack needs at least two packages")
	}
	type found struct {
		source int
		part   SplitPart
	}
	newest := map[string]found{}
	all := []found{}
	for i, path := range sources {
		reader, err := OpenFile(path)
		if err != nil {
			return 0, nil, FileCheck{}, err
		}
		pkg, err := openGamePackage(reader)
		if err == nil {
			var plan *SplitPlan
			if plan, err = planSplit(reader, pkg, 1); err == nil {
				for _, part := range plan.Parts {
					all = append(all, found{source: i, part: part})
					if best, ok := newest[part.TitleId]; !ok || part.Version > best.part.Version {
						newest[part.TitleId] = found{source: i, part: part}
					}
				}
			}
		}
		reader.Close()
		if err != nil {
			return 0, nil, FileCheck{}, err
		}
	}
	drop := make([]map[string]bool, len(sources))
	replaced := []string{}
	for _, f := range all {
		// the same version in another package is the same files, given once by the merge
		if f.part.Version >= newest[f.part.TitleId].part.Version {
			continue
		}
		if drop[f.source] == nil {
			drop[f.source] = map[string]bool{}
		}
		for _, file := range f.part.files {
			drop[f.source][file.name] = true
		}
		replaced = append(replaced, fmt.Sprintf("%s v%d", strings.ToUpper(f.part.TitleId), f.part.Version))
	}
	skipped, written, err := writePackages(ctx, sources, target, withoutDeltas, drop, progress)
	if err != nil {
		return 0, nil, FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return skipped, replaced, check, err
}

// StripDeltasAndCheck writes an NSP or NSZ again without the delta fragments of its updates,
// checked as MergeAndCheck checks a pack. It gives how many bytes were left out.
func StripDeltasAndCheck(ctx context.Context, source string, target string, progress func(done int64, total int64)) (int64, FileCheck, error) {
	skipped, written, err := writePackages(ctx, []string{source}, target, true, nil, progress)
	if err != nil {
		return 0, FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return skipped, check, err
}

// writePackages writes the files of the packages into one NSP or NSZ at target, but the ones
// drop lists, by the index of their package.
func writePackages(ctx context.Context, sources []string, target string, withoutDeltas bool, drop []map[string]bool, progress func(done int64, total int64)) (int64, []*writtenFile, error) {
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
			if seen[key] || (drop != nil && drop[len(opened)-1][file.name]) {
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
	defer endHashes(written)
	err = writer.begin()
	for i, p := range parts {
		if err != nil {
			break
		}
		if err = writer.startFile(); err != nil {
			break
		}
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
