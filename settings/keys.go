package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/magiconair/properties"
	"go.uber.org/zap"
)

// the keys loaded, read by tasks running in the background while they are loaded again
var keysInstance atomic.Pointer[switchKeys]

type switchKeys struct {
	keys map[string]string
	// title keys by lower case rights ID, from title.keys next to prod.keys
	titleKeys map[string]string
}

// TitleKey returns the encrypted title key of a rights ID from title.keys, if known.
// Title keys are needed to decrypt games without a ticket in their NSP.
func (k *switchKeys) TitleKey(rightsId string) (string, bool) {
	if k == nil {
		return "", false
	}
	key, ok := k.titleKeys[strings.ToLower(rightsId)]
	return key, ok
}

func (k *switchKeys) GetKey(keyName string) string {
	return k.keys[keyName]
}

func SwitchKeys() (*switchKeys, error) {
	return keysInstance.Load(), nil
}

// lastKeysReport is what was last reported about the keys, so it is not repeated
var (
	lastKeysReport  string
	keysReportMutex sync.Mutex
)

func InitSwitchKeys(baseFolder string) (*switchKeys, error) {
	// the keys in use stay while the new ones are read: a compression running meanwhile
	// does not find them missing. A failed lookup clears them below.
	var (
		path string
		p    *properties.Properties
		err  error
	)
	logger := zap.S()

	// first, try to read the prod keys from the settings value
	settings := ReadSettings(baseFolder)
	if settings.Prodkeys != "" {
		path = settings.Prodkeys
		path = resolveKeysPath(path)

		logger.Debugf("Trying to load prod.keys based on settings.json: %v", path)
		p, err = properties.LoadFile(path, properties.UTF8)
	} else {
		err = errors.New("prod.keys not defined in settings.json")
	}

	// second, if not found by settings look into the current folder
	if err != nil {
		path = filepath.Join(baseFolder, "prod.keys")

		logger.Debugf("Trying to load prod.keys based on current folder: %v", path)
		p, err = properties.LoadFile(path, properties.UTF8)
	}

	// third, if not found in current, look in home directory
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			err = homeErr
		} else {
			path = filepath.Join(home, ".switch", "prod.keys")

			logger.Debugf("Trying to load prod.keys based on home directory: %v", path)
			p, err = properties.LoadFile(path, properties.UTF8)
		}
	}

	if err != nil {
		// a failed lookup must not leave keys from a previous base folder active
		keysInstance.Store(nil)
		keysReportMutex.Lock()
		defer keysReportMutex.Unlock()
		if lastKeysReport != "none" {
			lastKeysReport = "none"
			logger.Info("Unable to find prod.keys")
		}
		return nil, errors.New("Error trying to read prod.keys [reason:" + err.Error() + "]")
	}

	loaded := &switchKeys{keys: map[string]string{}, titleKeys: map[string]string{}}
	for _, key := range p.Keys() {
		value, _ := p.Get(key)
		loaded.keys[key] = value
	}

	// optional: title keys for games whose NSP has no ticket
	titleKeysPath := filepath.Join(filepath.Dir(path), "title.keys")
	if titleKeys, err := properties.LoadFile(titleKeysPath, properties.UTF8); err == nil {
		for _, rightsId := range titleKeys.Keys() {
			value, _ := titleKeys.Get(rightsId)
			loaded.titleKeys[strings.ToLower(strings.TrimSpace(rightsId))] = strings.TrimSpace(value)
		}
	}

	keysInstance.Store(loaded)

	// the keys are read again before every synchronization: they are reported when they change
	report := fmt.Sprintf("%s|%d|%d|%s", path, len(loaded.keys), len(loaded.titleKeys), KeysFingerprint())
	keysReportMutex.Lock()
	defer keysReportMutex.Unlock()
	if report != lastKeysReport {
		lastKeysReport = report
		logger.Infof("Loaded prod.keys from: %v", path)
		if len(loaded.titleKeys) > 0 {
			logger.Infof("Loaded %v title keys from: %v", len(loaded.titleKeys), titleKeysPath)
		}
	}
	return loaded, nil
}

// resolveKeysPath accepts either a path to a .keys file or a folder containing prod.keys.
func resolveKeysPath(path string) string {
	if !strings.EqualFold(filepath.Ext(path), ".keys") {
		return filepath.Join(path, "prod.keys")
	}
	return path
}

// GetSwitchKeys reads the keys from a .keys file or a folder containing prod.keys,
// without changing the active keys.
func GetSwitchKeys(path string) (map[string]string, error) {
	keys := map[string]string{}

	p, err := properties.LoadFile(resolveKeysPath(path), properties.UTF8)
	if err != nil {
		return keys, err
	}

	for _, key := range p.Keys() {
		value, _ := p.Get(key)
		keys[key] = value
	}

	return keys, nil
}

func IsKeysFileAvailable() bool {
	if keys, _ := SwitchKeys(); keys != nil && keys.GetKey("header_key") != "" {
		return true
	}

	return false
}

// KeysFingerprint identifies the loaded keys without revealing them: a hash of the key
// names and values, or "" when no keys are loaded. It changes when keys are added,
// removed or updated, e.g. to rescan files that could not be decrypted before.
func KeysFingerprint() string {
	loaded := keysInstance.Load()
	if loaded == nil || len(loaded.keys) == 0 {
		return ""
	}
	names := make([]string, 0, len(loaded.keys))
	for name := range loaded.keys {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name + "=" + loaded.keys[name] + "\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}

// KeysSummary tells how many keys and title keys are loaded and the newest key generation,
// without revealing any key: newer games need keys of newer generations.
func KeysSummary() (keys int, titleKeys int, generation int) {
	loaded := keysInstance.Load()
	if loaded == nil {
		return 0, 0, -1
	}
	generation = -1
	for name := range loaded.keys {
		if suffix, ok := strings.CutPrefix(name, "key_area_key_application_"); ok {
			if value, err := strconv.ParseInt(suffix, 16, 32); err == nil && int(value) > generation {
				generation = int(value)
			}
		}
	}
	return len(loaded.keys), len(loaded.titleKeys), generation
}

// UseSwitchKeys makes the keys of a file the active ones: a prod.keys or a keys.txt as hactool
// reads them, or a folder holding one, with the title.keys next to it when there is one. It is
// for the tools that take the keys on their command line, as nsz does with --keys.
func UseSwitchKeys(path string) error {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		for _, name := range []string{"prod.keys", "keys.txt"} {
			if _, err := os.Stat(filepath.Join(path, name)); err == nil {
				path = filepath.Join(path, name)
				break
			}
		}
	}
	p, err := properties.LoadFile(path, properties.UTF8)
	if err != nil {
		return err
	}
	loaded := &switchKeys{keys: map[string]string{}, titleKeys: map[string]string{}}
	for _, key := range p.Keys() {
		value, _ := p.Get(key)
		loaded.keys[key] = value
	}
	if titleKeys, err := properties.LoadFile(filepath.Join(filepath.Dir(path), "title.keys"), properties.UTF8); err == nil {
		for _, rightsId := range titleKeys.Keys() {
			value, _ := titleKeys.Get(rightsId)
			loaded.titleKeys[strings.ToLower(strings.TrimSpace(rightsId))] = strings.TrimSpace(value)
		}
	}
	keysInstance.Store(loaded)
	return nil
}
