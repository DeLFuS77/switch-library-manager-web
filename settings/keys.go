package settings

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/magiconair/properties"
	"go.uber.org/zap"
)

var (
	keysInstance *switchKeys
)

type switchKeys struct {
	keys map[string]string
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

	keysInstance = &switchKeys{keys: map[string]string{}}
	for _, key := range p.Keys() {
		value, _ := p.Get(key)
		keysInstance.keys[key] = value
	}

	logger.Infof("Loaded prod.keys from: %v", path)
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
