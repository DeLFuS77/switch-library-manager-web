package switchfs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

// A multi-title NSP or NSZ (a "SuperNSP", or a complete pack) holds several titles: a game,
// its updates and its DLC. Each title keeps its own Meta NCA (CNMT), which lists the NCAs it
// is made of, and its own ticket and certificate, named after its rights ID (the title ID
// first). Splitting it writes one package per CNMT with the files of that title, copied as
// they are: nothing is decrypted, decompressed or changed, so every part holds the same bytes
// as the original had for it, and an NSZ gives NSZ parts with the same NCZ files.
//
// A game card dump (XCI or XCZ) with several games, like a collection on one cartridge,
// splits into one XCI (or XCZ) per game, with its update and DLC: the NCAs of a game card
// are made for a game card, and an NSP of them would need them signed again to install.
// Each part keeps the header of the original card and a secure partition with the files of
// its game, as an XCZ written by this app does.

// SplitPart is a title of a multi-title package and the files of the package it is made of.
type SplitPart struct {
	TitleId string
	// BASE, UPD or DLC
	Type    string
	Version int
	// the size of the package of the part, once written
	Size int64
	// it holds compressed files (NCZ): it is an NSZ (or an XCZ)
	Compressed bool
	// a game of a game card dump: its part is an XCI (or an XCZ) with the game, its update
	// and its DLC, under the title ID of the game
	Cartridge bool
	// the titles of the part: one, or those of a game of a game card
	Titles int
	files  []packedFile
	// the layout of the game card the part keeps
	card *gamePackage
}

// Files are the names of the files of the part, in the order of the original.
func (p SplitPart) Files() []string {
	names := make([]string, len(p.files))
	for i, file := range p.files {
		names[i] = file.name
	}
	return names
}

// SplitPlan is how a package is split.
type SplitPlan struct {
	Parts []SplitPart
	// files of the package no title lists (left out of the parts): with any NCA among them,
	// the original holds more than its parts and must be kept
	Unclaimed []string
}

// Complete tells whether every NCA, ticket and certificate of the package is in a part.
func (p *SplitPlan) Complete() bool {
	for _, name := range p.Unclaimed {
		switch strings.ToLower(extensionOf(name)) {
		case ".nca", ".ncz", ".tik", ".cert":
			return false
		}
	}
	return true
}

func extensionOf(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

// ErrSingleTitle is given for a package that holds one title only: there is nothing to split.
var ErrSingleTitle = errors.New("the file holds one title only")

// PlanSplit reads the CNMTs of an NSP or NSZ and gives the parts it splits into, in the order
// game, updates, DLC. The keys are needed to read the CNMTs.
func PlanSplit(path string) (*SplitPlan, error) {
	reader, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return nil, err
	}
	plan, err := planSplit(reader, pkg, 2)
	if err != nil || !pkg.xci {
		return plan, err
	}
	return byGame(plan, pkg)
}

// byGame joins the titles of a game card into one part per game: the game, its updates and
// its DLC. A card with one game is not split.
func byGame(plan *SplitPlan, pkg *gamePackage) (*SplitPlan, error) {
	games := map[string]*SplitPart{}
	order := []string{}
	for _, part := range plan.Parts {
		game, err := gameOf(part)
		if err != nil {
			return nil, err
		}
		joined, ok := games[game]
		if !ok {
			joined = &SplitPart{TitleId: game, Type: part.Type, Cartridge: true, card: pkg}
			games[game] = joined
			order = append(order, game)
		}
		joined.Titles++
		joined.Compressed = joined.Compressed || part.Compressed
		if part.Type == "BASE" {
			joined.Type = "BASE"
		}
		// named after the version it brings, as the shops name a card with its update
		if part.Type != "DLC" && part.Version > joined.Version {
			joined.Version = part.Version
		}
		joined.files = append(joined.files, part.files...)
	}
	if len(games) < 2 {
		return nil, ErrSingleTitle
	}
	sort.Strings(order)
	result := &SplitPlan{Unclaimed: plan.Unclaimed}
	for _, game := range order {
		part := games[game]
		// in the order of the original card
		sort.SliceStable(part.files, func(i, j int) bool { return part.files[i].offset < part.files[j].offset })
		part.Size = cardSize(pkg, part.files)
		result.Parts = append(result.Parts, *part)
	}
	return result, nil
}

// gameOf is the title ID of the game a title belongs to: an update is the ID of its game
// plus 0x800, a DLC the ID of its game plus 0x1000 and its number.
func gameOf(part SplitPart) (string, error) {
	id, err := strconv.ParseUint(part.TitleId, 16, 64)
	if err != nil {
		return "", fmt.Errorf("invalid title ID %q", part.TitleId)
	}
	if part.Type == "DLC" {
		id -= 0x1000
	}
	return fmt.Sprintf("%016x", id&^0xFFF), nil
}

// cardSize is the size of the game card xczWriter writes with these files: the header of
// the original card, the root HFS0, the other partitions left empty and the secure one.
func cardSize(pkg *gamePackage, files []packedFile) int64 {
	align := func(position int64) int64 { return (position + 0x1FF) &^ 0x1FF }
	position := pkg.rootOffset + hfs0ReservedHeader
	secure := indexOf(pkg.partitions, "secure")
	for i := range pkg.partitions {
		if i == secure {
			position += hfs0ReservedHeader
			for _, file := range files {
				position += file.size
			}
		} else {
			position += 0x200
		}
		position = align(position)
	}
	return position
}

// planSplit gives the titles of a package, with ErrSingleTitle when it holds fewer than
// minTitles.
func planSplit(reader io.ReaderAt, pkg *gamePackage, minTitles int) (*SplitPlan, error) {
	type title struct {
		part SplitPart
		// the content IDs of the title: those its CNMT lists and the one of the Meta NCA itself
		ids  map[string]bool
		meta string
	}
	titles := []*title{}
	seen := map[string]bool{}
	for _, file := range pkg.files {
		if !strings.HasSuffix(strings.ToLower(file.name), ".cnmt.nca") {
			continue
		}
		_, section, err := openMetaNcaDataSection(reader, file.offset)
		if err != nil {
			return nil, err
		}
		pfs0, err := readPfs0(bytes.NewReader(section), 0x0)
		if err != nil {
			return nil, err
		}
		attributes, err := readBinaryCnmt(pfs0, section)
		if err != nil {
			return nil, err
		}
		entries := cnmtEntries(pfs0, section)
		// the same title and version twice is one part
		key := fmt.Sprintf("%s/%d", attributes.TitleId, attributes.Version)
		if seen[key] {
			continue
		}
		seen[key] = true
		t := &title{part: SplitPart{TitleId: attributes.TitleId, Type: attributes.Type, Version: attributes.Version, Titles: 1}, ids: map[string]bool{}, meta: file.name}
		if id := contentId(file.name); id != "" {
			t.ids[id] = true
		}
		for _, entry := range entries {
			t.ids[entry.id] = true
		}
		titles = append(titles, t)
	}
	if len(titles) < minTitles {
		return nil, ErrSingleTitle
	}

	claimed := map[string]bool{}
	for _, t := range titles {
		for _, file := range pkg.files {
			lower := strings.ToLower(file.name)
			belongs := file.name == t.meta
			switch extensionOf(lower) {
			case ".tik", ".cert":
				// named after the rights ID, which starts with the title ID
				belongs = strings.HasPrefix(lower, t.part.TitleId)
			default:
				// NCAs, NCZs, and the XML and images some tools add, named after an NCA
				if id := contentId(file.name); id != "" && t.ids[id] {
					belongs = true
				}
			}
			if !belongs {
				continue
			}
			claimed[file.name] = true
			t.part.files = append(t.part.files, file)
			if strings.HasSuffix(lower, ".ncz") {
				t.part.Compressed = true
			}
		}
		t.part.Size = pfs0Size(t.part.files)
	}

	plan := &SplitPlan{}
	for _, t := range titles {
		plan.Parts = append(plan.Parts, t.part)
	}
	order := map[string]int{"BASE": 0, "UPD": 1, "DLC": 2}
	sort.SliceStable(plan.Parts, func(i, j int) bool {
		a, b := plan.Parts[i], plan.Parts[j]
		if order[a.Type] != order[b.Type] {
			return order[a.Type] < order[b.Type]
		}
		if a.TitleId != b.TitleId {
			return a.TitleId < b.TitleId
		}
		return a.Version < b.Version
	})
	for _, file := range pkg.files {
		if !claimed[file.name] {
			plan.Unclaimed = append(plan.Unclaimed, file.name)
		}
	}
	return plan, nil
}

// pfs0Size is the size of the PFS0 pfs0Writer writes with these files.
func pfs0Size(files []packedFile) int64 {
	header := int64(0x10 + PfsfileEntryTableSize*len(files))
	for _, file := range files {
		header += int64(len(file.name)) + 1
	}
	size := (header + 0x1F) &^ 0x1F
	for _, file := range files {
		size += file.size
	}
	return size
}

// WriteSplitPart writes a part of a package into a new NSP (or NSZ) at target, its files
// copied as they are in the order of the original. The target is removed if it cannot be
// written.
func WriteSplitPart(ctx context.Context, source string, part SplitPart, target string, progress func(done int64, total int64)) error {
	_, err := writeSplitPart(ctx, source, part, target, progress)
	return err
}

// WriteSplitPartAndCheck is WriteSplitPart that checks the part while writing it, instead of
// reading it again (see DecompressAndCheck); its NCZ files are still decompressed from the
// disk to be checked. On an error the part is removed.
func WriteSplitPartAndCheck(ctx context.Context, source string, part SplitPart, target string, progress func(done int64, total int64)) (FileCheck, error) {
	written, err := writeSplitPart(ctx, source, part, target, progress)
	if err != nil {
		return FileCheck{}, err
	}
	check, err := checkWritten(ctx, target, written)
	if err != nil {
		os.Remove(target)
	}
	return check, err
}

func writeSplitPart(ctx context.Context, source string, part SplitPart, target string, progress func(done int64, total int64)) ([]*writtenFile, error) {
	if len(part.files) == 0 {
		return nil, errors.New("the part has no files")
	}
	reader, err := OpenFile(source)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	var total, done int64
	for _, file := range part.files {
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
	var writer packageWriter = &pfs0Writer{file: output, names: part.Files()}
	if part.card != nil {
		writer = &xczWriter{file: output, input: reader, pkg: part.card, names: part.Files()}
	}
	names := part.Files()
	written := make([]*writtenFile, len(part.files))
	err = writer.begin()
	for i, file := range part.files {
		if err != nil {
			break
		}
		writer.startFile()
		written[i] = newWrittenFile(names[i])
		err = copyRange(ctx, io.MultiWriter(output, written[i]), reader, file.offset, file.size, report)
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
