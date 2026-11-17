package settings

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"
)

// made-up keys: what matters is that each key comes out of the others as the console makes it
func madeUpKeys(t *testing.T) map[string]string {
	t.Helper()
	b := func(n int, fill byte) []byte { return []byte(strings.Repeat(string(rune(fill)), n)) }
	kekSource, keySource, areaSource := b(16, 'a'), b(16, 'b'), b(16, 'c')
	headerKekSource, headerKeySource := b(16, 'd'), b(32, 'e')
	keys := map[string]string{"aes_kek_generation_source": hex.EncodeToString(kekSource), "aes_key_generation_source": hex.EncodeToString(keySource),
		"key_area_key_application_source": hex.EncodeToString(areaSource), "header_kek_source": hex.EncodeToString(headerKekSource),
		"header_key_source": hex.EncodeToString(headerKeySource)}
	for i, name := range []string{"00", "01", "12"} {
		master := b(16, byte('f'+i))
		keys["master_key_"+name] = hex.EncodeToString(master)
		area, err := generateKek(areaSource, master, kekSource, keySource)
		if err != nil {
			t.Fatal(err)
		}
		keys["key_area_key_application_"+name] = hex.EncodeToString(area)
	}
	kek, _ := generateKek(headerKekSource, b(16, 'f'), kekSource, keySource)
	header, _ := ecbDecrypt(kek, headerKeySource)
	keys["header_key"] = hex.EncodeToString(header)
	return keys
}

// Keys that agree are checked and found right, with the firmware of their newest generation; a
// key changed by one digit is told by its master key.
func TestCheckKeys(t *testing.T) {
	keys := madeUpKeys(t)
	check := checkKeys(keys)
	if check.Generation != 0x12 || check.Firmware != "19.0.0" || check.Checked != 4 || len(check.Wrong) != 0 {
		t.Fatalf("keys that agree: %+v", check)
	}
	broken := keys["key_area_key_application_01"]
	keys["key_area_key_application_01"] = "0" + broken[1:]
	if broken[0] == '0' {
		keys["key_area_key_application_01"] = "1" + broken[1:]
	}
	keys["header_key"] = strings.Repeat("00", 32)
	if check := checkKeys(keys); !reflect.DeepEqual(check.Wrong, []string{"header_key", "master_key_01"}) {
		t.Fatalf("keys that do not agree: %+v", check)
	}
	// without the sources nothing is compared, and the generation is still told
	if check := checkKeys(map[string]string{"key_area_key_application_0c": "00"}); check.Checked != 0 || check.Generation != 0x0c || check.Firmware != "13.0.0" {
		t.Fatalf("without sources: %+v", check)
	}
	if KeyGenerationFirmware(40) != "" || checkKeys(nil).Generation != -1 {
		t.Fatal("a generation not known, or no keys")
	}
}
