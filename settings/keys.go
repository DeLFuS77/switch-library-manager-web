package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/magiconair/properties"
	"go.uber.org/zap"
)

var (
	keysInstance *switchKeys
)

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
	return keysInstance, nil
}

func InitSwitchKeys(baseFolder string) (*switchKeys, error) {
	// A failed lookup must not leave keys from a previous base folder active.
	keysInstance = nil
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

		logger.Infof("Trying to load prod.keys based on settings.json: %v", path)
		p, err = properties.LoadFile(path, properties.UTF8)
	} else {
		err = errors.New("prod.keys not defined in settings.json")
	}

	// second, if not found by settings look into the current folder
	if err != nil {
		path = filepath.Join(baseFolder, "prod.keys")

		logger.Infof("Trying to load prod.keys based on current folder: %v", path)
		p, err = properties.LoadFile(path, properties.UTF8)
	}

	// third, if not found in current, look in home directory
	if err != nil {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			err = homeErr
		} else {
			path = filepath.Join(home, ".switch", "prod.keys")

			logger.Infof("Trying to load prod.keys based on home directory: %v", path)
			p, err = properties.LoadFile(path, properties.UTF8)
		}
	}

	if err != nil {
		logger.Info("Unable to find prod.keys")
		return nil, errors.New("Error trying to read prod.keys [reason:" + err.Error() + "]")
	}

	keysInstance = &switchKeys{keys: map[string]string{}, titleKeys: map[string]string{}}
	for _, key := range p.Keys() {
		value, _ := p.Get(key)
		keysInstance.keys[key] = value
	}

	logger.Infof("Loaded prod.keys from: %v", path)

	// optional: title keys for games whose NSP has no ticket
	titleKeysPath := filepath.Join(filepath.Dir(path), "title.keys")
	if titleKeys, err := properties.LoadFile(titleKeysPath, properties.UTF8); err == nil {
		for _, rightsId := range titleKeys.Keys() {
			value, _ := titleKeys.Get(rightsId)
			keysInstance.titleKeys[strings.ToLower(strings.TrimSpace(rightsId))] = strings.TrimSpace(value)
		}
		logger.Infof("Loaded %v title keys from: %v", len(keysInstance.titleKeys), titleKeysPath)
	}
	return keysInstance, nil
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
	if keysInstance == nil || len(keysInstance.keys) == 0 {
		return ""
	}
	names := make([]string, 0, len(keysInstance.keys))
	for name := range keysInstance.keys {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		hash.Write([]byte(name + "=" + keysInstance.keys[name] + "\n"))
	}
	return hex.EncodeToString(hash.Sum(nil))[:16]
}
