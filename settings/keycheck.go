package settings

import (
	"crypto/aes"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// KeysCheck tells how far the keys loaded go and whether they agree with each other, without
// telling any of them: the newest key generation, the firmware that brought it, and the keys
// that do not match the keys they are made of. prod.keys as the dumpers write it holds the
// master keys with the sources the other keys are made of: a key that does not come out of them
// was damaged, or mixed from another dump.
type KeysCheck struct {
	// the newest generation of key_area_key_application_XX, -1 without keys
	Generation int
	// the firmware that brought that generation, "" when it is newer than the ones known here
	Firmware string
	// the keys compared with what their master key makes, and the ones that did not match
	Checked int
	Wrong   []string
}

// keyGenerationFirmware is the firmware that brought each key generation (its master key).
var keyGenerationFirmware = []string{"1.0.0", "3.0.0", "3.0.1", "4.0.0", "5.0.0", "6.0.0", "6.2.0", "7.0.0", "8.1.0", "9.0.0", "9.1.0",
	"12.1.0", "13.0.0", "14.0.0", "15.0.0", "16.0.0", "17.0.0", "18.0.0", "19.0.0", "20.0.0"}

// KeyGenerationFirmware is the firmware that brought a key generation, "" for one newer than the
// ones known here.
func KeyGenerationFirmware(generation int) string {
	if generation < 0 || generation >= len(keyGenerationFirmware) {
		return ""
	}
	return keyGenerationFirmware[generation]
}

// CheckKeys checks the keys loaded (see KeysCheck).
func CheckKeys() KeysCheck {
	loaded := keysInstance.Load()
	if loaded == nil {
		return KeysCheck{Generation: -1}
	}
	return checkKeys(loaded.keys)
}

func checkKeys(keys map[string]string) KeysCheck {
	check := KeysCheck{Generation: -1}
	value := func(name string) []byte {
		data, err := hex.DecodeString(strings.TrimSpace(keys[name]))
		if err != nil {
			return nil
		}
		return data
	}
	for name := range keys {
		if suffix, ok := strings.CutPrefix(name, "key_area_key_application_"); ok {
			if generation, err := strconv.ParseInt(suffix, 16, 32); err == nil && int(generation) > check.Generation {
				check.Generation = int(generation)
			}
		}
	}
	check.Firmware = KeyGenerationFirmware(check.Generation)
	kekSource, keySource := value("aes_kek_generation_source"), value("aes_key_generation_source")
	if len(kekSource) != 16 {
		return check
	}
	// the key area key of each generation comes out of its master key
	if areaSource := value("key_area_key_application_source"); len(areaSource) == 16 {
		for name := range keys {
			suffix, ok := strings.CutPrefix(name, "master_key_")
			if !ok || len(suffix) != 2 {
				continue
			}
			want := value("key_area_key_application_" + suffix)
			master := value(name)
			if len(want) != 16 || len(master) != 16 {
				continue
			}
			got, err := generateKek(areaSource, master, kekSource, keySource)
			if err != nil {
				continue
			}
			check.Checked++
			if !equalBytes(got, want) {
				check.Wrong = append(check.Wrong, name)
			}
		}
	}
	// and the key of the headers out of the first one
	if headerKekSource, headerKeySource, master, want := value("header_kek_source"), value("header_key_source"), value("master_key_00"), value("header_key"); len(headerKekSource) == 16 && len(headerKeySource) == 32 && len(master) == 16 && len(want) == 32 {
		if kek, err := generateKek(headerKekSource, master, kekSource, keySource); err == nil {
			if got, err := ecbDecrypt(kek, headerKeySource); err == nil {
				check.Checked++
				if !equalBytes(got, want) {
					check.Wrong = append(check.Wrong, "header_key")
				}
			}
		}
	}
	sort.Strings(check.Wrong)
	return check
}

// generateKek makes a key out of a master key, as the console does: the master key opens the
// key encryption key, which opens the source, which opens the key seed when there is one.
func generateKek(source, masterKey, kekSeed, keySeed []byte) ([]byte, error) {
	kek, err := ecbDecrypt(masterKey, kekSeed)
	if err != nil {
		return nil, err
	}
	sourceKek, err := ecbDecrypt(kek, source)
	if err != nil {
		return nil, err
	}
	if len(keySeed) != 16 {
		return sourceKek, nil
	}
	return ecbDecrypt(sourceKek, keySeed)
}

func ecbDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("%d bytes are not whole blocks", len(data))
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return out, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
