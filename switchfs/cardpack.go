package switchfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// PackIntoCard writes a game card dump (an XCI or XCZ) again with the files of other
// packages (the NSP or NSZ of its updates and DLC) in its secure partition, as the cards
// sold with an update hold it: the header of the card is kept, its other partitions are left
// empty as an XCZ has them, and the delta fragments of the updates are left out. Of a title
// found twice, its newest version is kept. The result is an XCZ when it holds an NCZ. It is
// checked as it is written, as MergeAndCheck checks a pack. It gives the titles replaced.
func PackIntoCard(ctx context.Context, card string, extras []string, target string, progress func(done int64, total int64)) ([]string, FileCheck, error) {
	if len(extras) == 0 {
		return nil, FileCheck{}, errors.New("give the updates or DLC to put in the game card")
	}
	sources := append([]string{card}, extras...)
	type source struct {
		reader ReadAtCloser
		pkg    *gamePackage
	}
	opened := []source{}
	defer func() {
		for _, s := range opened {
			s.reader.Close()
		}
	}()
	type found struct {
		source int
		part   SplitPart
	}
	newest := map[string]found{}
	all := []found{}
	for i, path := range sources {
		reader, err := OpenFile(path)
		if err != nil {
			return nil, FileCheck{}, err
		}
		pkg, err := openGamePackage(reader)
		if err != nil {
			reader.Close()
			return nil, FileCheck{}, err
		}
		opened = append(opened, source{reader: reader, pkg: pkg})
		if i == 0 && !pkg.xci {
			return nil, FileCheck{}, errors.New("the first file must be a game card dump (XCI or XCZ)")
		}
		if i > 0 && pkg.xci {
			return nil, FileCheck{}, errors.New("only one game card can be packed: give its updates and DLC as NSP or NSZ")
		}
		plan, err := planSplit(reader, pkg, 1)
		if err != nil {
			return nil, FileCheck{}, fmt.Errorf("%s: %w", path, err)
		}
		for _, part := range plan.Parts {
			all = append(all, found{source: i, part: part})
			if best, ok := newest[part.TitleId]; !ok || part.Version > best.part.Version {
				newest[part.TitleId] = found{source: i, part: part}
			}
		}
	}
	drop := make([]map[string]bool, len(sources))
	replaced := []string{}
	for _, f := range all {
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

	type entry struct {
		source int
		file   packedFile
	}
	entries := []entry{}
	names := []string{}
	seen := map[string]bool{}
	var total int64
	for i, s := range opened {
		deltas := deltaFragmentNames(s.reader, s.pkg)
		for _, file := range s.pkg.files {
			key := strings.ToLower(file.name)
			if id := contentId(file.name); id != "" && (strings.HasSuffix(key, ".nca") || strings.HasSuffix(key, ".ncz")) {
				key = id
			}
			if seen[key] || deltas[file.name] || (drop[i] != nil && drop[i][file.name]) {
				continue
			}
			seen[key] = true
			entries = append(entries, entry{source: i, file: file})
			names = append(names, file.name)
			total += file.size
		}
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
		return nil, FileCheck{}, err
	}
	writer := &xczWriter{file: output, input: opened[0].reader, pkg: opened[0].pkg, names: names}
	written := make([]*writtenFile, len(entries))
	err = writer.begin()
	for i, e := range entries {
		if err != nil {
			break
		}
		writer.startFile()
		written[i] = newWrittenFile(names[i])
		err = copyRange(ctx, io.MultiWriter(output, written[i]), opened[e.source].reader, e.file.offset, e.file.size, report)
		if err == nil {
			err = writer.endFile()
		}
	}
	if err == nil {
		err = writer.finish()
	}
	if closeErr := output.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(target)
		return nil, FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return replaced, check, err
}
