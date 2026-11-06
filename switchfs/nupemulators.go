package switchfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// Eglantine and Lichii, the emulators that read NUP files (Lichii is a fork of Eglantine, with the
// same reader), are stricter than the format in a few things, and refuse a whole NUP that does
// not meet them: packages of kind 3 or 4 and flags other than the first, files with codec flags,
// files of 0 bytes that point at data, children of folders marked with the high bit, folders
// inside themselves or more than 128 deep, records of languages that run past their record. To
// start its game they take the ExeFS of the newest update, else the one of the game, and need
// main and main.npdm in it. NXPress tells the same (its internal/nup/emulators.go).

// NupEmulatorsRefuseToo is said of a NUP this reader does not read for a reason Eglantine and
// Lichii refuse it too.
const NupEmulatorsRefuseToo = "Eglantine and Lichii do not open it either"

// nupRefusedError is a NUP this reader does not read, which Eglantine and Lichii refuse too.
type nupRefusedError struct {
	error
}

func (e nupRefusedError) Unwrap() error {
	return e.error
}

func nupRefused(err error) error {
	return nupRefusedError{err}
}

// NupEmulatorsRefuse tells whether Eglantine and Lichii refuse too a NUP ReadNup did not read.
func NupEmulatorsRefuse(err error) bool {
	var refused nupRefusedError
	return errors.As(err, &refused)
}

// NupEmulatorIssue is one thing that keeps Eglantine and Lichii from a NUP: an English text with
// %v for its values, which is also the key of its translations.
type NupEmulatorIssue struct {
	Text string   `json:"text"`
	Args []string `json:"args,omitempty"`
}

// the texts of what keeps the emulators from a NUP, with %v for their values, which are also the
// keys of their translations
const (
	nupIssueKind          = "package %v is of kind %v, which the format keeps for later"
	nupIssueFlags         = "package %v has the flags %v, and only the first is known"
	nupIssueLocalized     = "the names or icons by language of package %v run past their record"
	nupIssueCodecFlag     = "a file has codec flags, which version 1 does not have"
	nupIssueCodecFlags    = "%v files have codec flags, which version 1 does not have"
	nupIssuePlacedEmpty   = "a file of 0 bytes points at data, where its offset and size must be 0"
	nupIssuePlacedEmpties = "%v files of 0 bytes point at data, where their offset and size must be 0"
	nupIssueTagged        = "its folders mark their files with the high bit, as a build of the packer of the format did"
	nupIssueLoop          = "the folder “%v” is inside itself"
	nupIssueDeep          = "its folders go more than 128 deep"
	nupIssueDlcOnly       = "it holds no game or update, and they do not load DLC from NUP files yet"
	nupIssueNoExefs       = "no game or update in it has an ExeFS"
	nupIssueGameLacks     = "the ExeFS of the game lacks: %v"
	nupIssueUpdateLacks   = "the ExeFS of update v%v lacks: %v"
)

// NupEmulatorTexts are the texts of NupEmulatorIssue, for their translations.
var NupEmulatorTexts = []string{nupIssueKind, nupIssueFlags, nupIssueLocalized, nupIssueCodecFlag, nupIssueCodecFlags, nupIssuePlacedEmpty, nupIssuePlacedEmpties, nupIssueTagged, nupIssueLoop, nupIssueDeep, nupIssueDlcOnly, nupIssueNoExefs, nupIssueGameLacks, nupIssueUpdateLacks}

func nupIssue(text string, args ...any) NupEmulatorIssue {
	issue := NupEmulatorIssue{Text: text}
	for _, arg := range args {
		issue.Args = append(issue.Args, fmt.Sprint(arg))
	}
	return issue
}

// Values are the values of the issue, for a text with %v.
func (i NupEmulatorIssue) Values() []any {
	values := make([]any, len(i.Args))
	for n, arg := range i.Args {
		values[n] = arg
	}
	return values
}

func (i NupEmulatorIssue) String() string {
	return fmt.Sprintf(i.Text, i.Values()...)
}

// NupEmulators is what Eglantine and Lichii make of a NUP.
type NupEmulators struct {
	// why they do not open it at all, none when they open it
	Refused []NupEmulatorIssue `json:"refused,omitempty"`
	// why they cannot start its game, nil when they can
	NoStart *NupEmulatorIssue `json:"no_start,omitempty"`
}

// Ok tells a NUP they open and whose game they can start.
func (e NupEmulators) Ok() bool {
	return len(e.Refused) == 0 && e.NoStart == nil
}

// the starts of what is said of a NUP the emulators do not read well, before its reasons
const (
	NupEmulatorsDoNotOpen   = "Eglantine and Lichii do not open it: %v"
	NupEmulatorsCannotStart = "Eglantine and Lichii cannot start it: %v"
)

// String tells, in English, what keeps them from the NUP; "" when nothing does.
func (e NupEmulators) String() string {
	switch {
	case len(e.Refused) > 0:
		reasons := make([]string, len(e.Refused))
		for i, issue := range e.Refused {
			reasons[i] = issue.String()
		}
		return fmt.Sprintf(NupEmulatorsDoNotOpen, strings.Join(reasons, "; "))
	case e.NoStart != nil:
		return fmt.Sprintf(NupEmulatorsCannotStart, e.NoStart)
	}
	return ""
}

// Emulators tells whether Eglantine and Lichii open a NUP and can start its game.
func (container *NupContainer) Emulators() NupEmulators {
	result := NupEmulators{Refused: append([]NupEmulatorIssue{}, container.strict...)}
	if issue := container.treeFault(); issue != nil {
		result.Refused = append(result.Refused, *issue)
	}
	if len(result.Refused) == 0 {
		result.Refused = nil
	}
	result.NoStart = container.startFault()
	return result
}

// treeFault tells a folder of the table inside itself, or one more than 128 deep, among all the
// folders of the table, not only the ones of the packages, as the emulators look at them.
func (container *NupContainer) treeFault() *NupEmulatorIssue {
	const (
		unseen = iota
		open
		done
	)
	state := make([]byte, len(container.Dirs))
	height := make([]int, len(container.Dirs))
	dirCount := uint32(len(container.Dirs))
	type frame struct {
		dir, next uint32
	}
	for start := range container.Dirs {
		if state[start] != unseen {
			continue
		}
		state[start] = open
		stack := []frame{{dir: uint32(start)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			dir := container.Dirs[top.dir]
			if top.next == dir.ChildCount {
				state[top.dir] = done
				finished := top.dir
				stack = stack[:len(stack)-1]
				if len(stack) > 0 {
					parent := stack[len(stack)-1].dir
					height[parent] = higher(height[parent], height[finished]+1)
				}
				continue
			}
			child := container.Children[dir.FirstChild+top.next]
			top.next++
			if child >= dirCount {
				continue
			}
			switch state[child] {
			case open:
				issue := nupIssue(nupIssueLoop, container.Dirs[child].Name)
				return &issue
			case done:
				height[top.dir] = higher(height[top.dir], height[child]+1)
			default:
				state[child] = open
				stack = append(stack, frame{dir: child})
			}
			if len(stack)+height[child] > nupMaxDepth {
				issue := nupIssue(nupIssueDeep)
				return &issue
			}
		}
	}
	return nil
}

// higher is the greater of two heights (the max of this package compares bytes).
func higher(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

// startFault tells why the emulators cannot start the game of a NUP, nil when they can: they
// take the ExeFS of its newest update when it has one, else the one of its game (an update stored
// without its game starts on its own), and need main and main.npdm in it, with those names.
func (container *NupContainer) startFault() *NupEmulatorIssue {
	base, update := -1, -1
	for i, pkg := range container.Packages {
		switch {
		case pkg.Kind == NupKindBase && base < 0:
			base = i
		case pkg.Kind == NupKindUpdate && (update < 0 || pkg.Version > container.Packages[update].Version):
			update = i
		}
	}
	hasExefs := func(pkg int) bool {
		if pkg < 0 {
			return false
		}
		exefs := container.Packages[pkg].ExefsDir
		return exefs != nupNoDir && int(exefs) < len(container.Dirs) && container.Dirs[exefs].ChildCount > 0
	}
	exefs := -1
	switch {
	case hasExefs(base) && hasExefs(update):
		exefs = update
	case hasExefs(base):
		exefs = base
	case hasExefs(update):
		exefs = update
	case base < 0 && update < 0:
		issue := nupIssue(nupIssueDlcOnly)
		return &issue
	default:
		issue := nupIssue(nupIssueNoExefs)
		return &issue
	}
	names := map[string]bool{}
	dir := container.Dirs[container.Packages[exefs].ExefsDir]
	for _, node := range container.Children[dir.FirstChild : dir.FirstChild+dir.ChildCount] {
		if node >= uint32(len(container.Dirs)) {
			names[container.Files[node-uint32(len(container.Dirs))].Name] = true
		}
	}
	missing := []string{}
	for _, name := range []string{"main", "main.npdm"} {
		if !names[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	pkg := container.Packages[exefs]
	issue := nupIssue(nupIssueGameLacks, strings.Join(missing, ", "))
	if pkg.Kind == NupKindUpdate {
		issue = nupIssue(nupIssueUpdateLacks, pkg.Version, strings.Join(missing, ", "))
	}
	return &issue
}

// nupStrictLocalized tells whether the records of names and icons by language of a block of
// metadata, as its types are written (9 and 10), stay in their record as the emulators read
// them: a length, a tag, a length and the data.
func nupStrictLocalized(block []byte) bool {
	for at := 0; at+nupTlvHeadSize <= len(block); {
		kind := binary.LittleEndian.Uint16(block[at:])
		length := int(binary.LittleEndian.Uint32(block[at+4:]))
		at += nupTlvHeadSize
		if length < 0 || length > len(block)-at {
			return true
		}
		payload := block[at : at+length]
		at += length
		if kind != nupTlvLocalizedTitles && kind != nupTlvLocalizedIcons {
			continue
		}
		for pos := 0; pos < len(payload); {
			tag := int(payload[pos])
			pos++
			if pos+tag+4 > len(payload) {
				return false
			}
			pos += tag
			size := int(binary.LittleEndian.Uint32(payload[pos:]))
			pos += 4
			if size < 0 || size > len(payload)-pos {
				return false
			}
			pos += size
		}
	}
	return true
}
