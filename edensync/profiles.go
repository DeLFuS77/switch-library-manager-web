package edensync

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// The profiles of the emulator, as Eden keeps them, and where the save of a game of a profile is.
//
// The profiles are in the system save of the account service, nand/system/save/8000000000000010/
// su/avators/profiles.dat (src/core/hle/service/acc/profile_manager.cpp,
// https://git.eden-emu.dev/eden-emu/eden): 0x650 bytes, 0x10 of padding and then 8 entries of
// 0xC8 bytes, each:
//
//	0x00  16  the UUID of the user (all zero: an empty entry)
//	0x10  16  the UUID again
//	0x20   8  the time it was made (little endian)
//	0x28  32  the name, UTF-8, padded with zeros
//	0x48 128  data of the console (the icon and its colour)
//
// The save of a game of a profile is, in the NAND folder (src/core/file_sys/savedata_factory.cpp,
// GetFullPath), user/save/0000000000000000/<user id>/<title ID>, where the user id is the 16 bytes
// of the UUID read as two little-endian 64-bit numbers, the second written first, each as 16
// uppercase hexadecimal digits: the bytes of the UUID from the last to the first. Newer builds use
// user/save/account/<UUID as 32 lowercase hexadecimal digits, first byte first>/<title ID>/0 when
// that folder exists (GetFutureSaveDataPath). Eden keeps the size of a save in .yuzu_save_size,
// in the folder of the save: a file of the emulator, not of the game.

const (
	profilesPath     = "system/save/8000000000000010/su/avators/profiles.dat"
	profilesSize     = 0x650
	profilesHeader   = 0x10
	profileEntrySize = 0xC8
	profileNameAt    = 0x28
	profileNameSize  = 0x20
	maxProfiles      = 8
	// the folder of the saves of the users that are not system saves
	userSaveFolder = "0000000000000000"
)

// ProfileID is the UUID of a profile of the emulator, its 16 bytes as Eden keeps them.
type ProfileID [16]byte

var (
	formattedId = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	rawId       = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)
	titleId     = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)
)

// ErrProfileID tells a text that is not the UUID of a profile.
var ErrProfileID = errors.New("not the UUID of a profile")

// String is the UUID as Eden shows it (UUID::FormattedString): 8-4-4-4-12 lowercase hexadecimal
// digits, the first byte first. It names the profile to the library.
func (id ProfileID) String() string {
	text := hex.EncodeToString(id[:])
	return text[:8] + "-" + text[8:12] + "-" + text[12:16] + "-" + text[16:20] + "-" + text[20:]
}

// Raw is the UUID as 32 lowercase hexadecimal digits, the first byte first (UUID::RawString).
func (id ProfileID) Raw() string {
	return hex.EncodeToString(id[:])
}

// UserFolder is the folder of the saves of the profile, as yuzu names it: the two halves of the
// UUID as little-endian numbers, the second first, in uppercase.
func (id ProfileID) UserFolder() string {
	low := binary.LittleEndian.Uint64(id[:8])
	high := binary.LittleEndian.Uint64(id[8:])
	return fmt.Sprintf("%016X%016X", high, low)
}

// IsZero tells an empty entry.
func (id ProfileID) IsZero() bool {
	return id == ProfileID{}
}

// ParseProfileID reads a UUID written as String or Raw write it.
func ParseProfileID(text string) (ProfileID, error) {
	text = strings.TrimSpace(text)
	if formattedId.MatchString(text) {
		text = strings.ReplaceAll(text, "-", "")
	} else if !rawId.MatchString(text) {
		return ProfileID{}, ErrProfileID
	}
	var id ProfileID
	if _, err := hex.Decode(id[:], []byte(text)); err != nil {
		return ProfileID{}, ErrProfileID
	}
	if id.IsZero() {
		return ProfileID{}, ErrProfileID
	}
	return id, nil
}

// Profile is a profile of the emulator.
type Profile struct {
	ID   ProfileID
	Name string
}

// ErrProfiles tells a file of profiles that is not one.
var ErrProfiles = errors.New("profiles.dat is not a file of profiles")

// ParseProfiles reads the profiles of profiles.dat, in their order, without the empty entries.
func ParseProfiles(data []byte) ([]Profile, error) {
	if len(data) < profilesSize {
		return nil, ErrProfiles
	}
	profiles := []Profile{}
	for i := 0; i < maxProfiles; i++ {
		entry := data[profilesHeader+i*profileEntrySize : profilesHeader+(i+1)*profileEntrySize]
		var id ProfileID
		copy(id[:], entry[:16])
		if id.IsZero() {
			continue
		}
		name := entry[profileNameAt : profileNameAt+profileNameSize]
		if end := strings.IndexByte(string(name), 0); end >= 0 {
			name = name[:end]
		}
		profiles = append(profiles, Profile{ID: id, Name: strings.TrimSpace(strings.ToValidUTF8(string(name), ""))})
	}
	return profiles, nil
}

// ProfilesFile writes profiles.dat with these profiles, as Eden writes it: for tests, and for the
// programs that test with a made-up folder of the emulator.
func ProfilesFile(profiles []Profile) []byte {
	data := make([]byte, profilesSize)
	for i, profile := range profiles {
		if i == maxProfiles {
			break
		}
		entry := data[profilesHeader+i*profileEntrySize:]
		copy(entry[:16], profile.ID[:])
		copy(entry[16:32], profile.ID[:])
		name := profile.Name
		for len(name) > profileNameSize {
			_, size := utf8.DecodeLastRuneInString(name)
			name = name[:len(name)-size]
		}
		copy(entry[profileNameAt:profileNameAt+profileNameSize], name)
	}
	return data
}

// Profiles reads the profiles of the emulator.
func (f Folder) Profiles() ([]Profile, error) {
	data, err := os.ReadFile(filepath.Join(f.Nand, filepath.FromSlash(profilesPath)))
	if err != nil {
		return nil, err
	}
	return ParseProfiles(data)
}

// NormalTitleID is a title ID in 16 uppercase hexadecimal digits, if it is one.
func NormalTitleID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if !titleId.MatchString(id) {
		return "", false
	}
	return strings.ToUpper(id), true
}

// legacySaveFolder is the folder of a save as yuzu has it.
func (f Folder) legacySaveFolder(profile ProfileID, title string) string {
	return filepath.Join(f.Nand, "user", "save", userSaveFolder, profile.UserFolder(), title)
}

// futureSaveFolder is the folder of a save as newer builds of Eden have it: by the title ID
// without its last byte.
func (f Folder) futureSaveFolder(profile ProfileID, title string) string {
	masked := title[:14] + "00"
	return filepath.Join(f.Nand, "user", "save", "account", profile.Raw(), masked, "0")
}

// SaveFolder is the folder of the save of a game of a profile, as Eden opens it: the newer one
// when it is there, else the one of yuzu. It may not exist: the game was never opened with that
// profile. The title ID is 16 hexadecimal digits.
func (f Folder) SaveFolder(profile ProfileID, title string) string {
	title = strings.ToUpper(title)
	if future := f.futureSaveFolder(profile, title); isDir(future) {
		return future
	}
	return f.legacySaveFolder(profile, title)
}

// Games are the title IDs of the games with a save folder for a profile, in both places.
func (f Folder) Games(profile ProfileID) ([]string, error) {
	found := map[string]bool{}
	for _, folder := range []string{filepath.Join(f.Nand, "user", "save", userSaveFolder, profile.UserFolder()), filepath.Join(f.Nand, "user", "save", "account", profile.Raw())} {
		entries, err := os.ReadDir(folder)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			if id, ok := NormalTitleID(entry.Name()); ok && entry.IsDir() {
				found[id] = true
			}
		}
	}
	games := make([]string, 0, len(found))
	for id := range found {
		games = append(games, id)
	}
	sort.Strings(games)
	return games, nil
}
