package switchfs

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// ncaHeaderModuli are the public keys Nintendo signs the header of every NCA with (its first
// signature), by the generation the header names at 0x221. They are public: tools that check
// game files carry them.
var ncaHeaderModuli = []string{
	"bfbe406cf4a780e9f07d0c99611d772f96bc4b9e58381b03abb175499f2b4d5834b005a37522be1a3f0373ac7068d116b904465eb707912f078b26def60007b2b451f80d0a5e58adebbc9ad649b964efa782b5cf6d7013b00f85f6a908aa4d676687fa89ff7590181e6b3de98a68c92604d980ce3f5e92ce01ff063bf2c1a90cce026f16bc92420a4164cd52b6344daec02edea4df27683cc1a060ad43f3fc86c13e6c46f77c299ffafdf0e3ce64e735f2f656566f6df1e242b08340a5c3202bcc9aaecaed4d7030a8701c70fd1363290279ead2a7af3528321c7be62f1aaa407e328c2742fe8278ec0debe6834b6d8104401a9e9a67f67229fa04f09de4f403",
	"ade3e1fa0435e5b6dd49ea8929b1ffb643dfca96a04a13df43d9949796436548705833a27d357b96745e0b5c32181424c258b36c227aa1b7cb90a7a3f97d4516a5c8ed8fad395e9e4b51687df80c35c63f91ae44a592300d46f840ffd0ff06d21c7f9618dcb71d663ed173bc158a2f94f300c183f1cdd78188abdf8cef97dd1b175f58f69ae9e8c22f3815f52107f837905d2e024024150d25b7265d09cc4cf4f21b94705a9eeeed7777d45199f5dc761ee36c8cd112d457d1b683e4e4fedae9b43b33e5378adfb57f89f19b9eb015b23afeea61845b7d4b23120b8312f2226bb922964b260b635e965752a3676422cad0563e74b5981f0df8b334e698685aad",
}

// SignatureCheck tells whether the NCA files of a game are signed by Nintendo: the ones
// checked, and by name the ones whose header is not, as a tool that rewrote them leaves it,
// or whose key is not known.
type SignatureCheck struct {
	Checked  int
	Unsigned []string
	Unknown  []string
}

// CheckSignatures checks the first signature of the header of every NCA (and NCZ) of an NSP,
// NSZ, XCI or XCZ: RSA-PSS with SHA-256 over 0x200-0x3FF, by the public key of Nintendo. It
// reads the headers only.
func CheckSignatures(path string) (SignatureCheck, error) {
	check := SignatureCheck{}
	keys, _ := settings.SwitchKeys()
	if keys == nil || keys.GetKey("header_key") == "" {
		return check, &MissingKeyError{KeyName: "header_key"}
	}
	reader, err := OpenFile(path)
	if err != nil {
		return check, err
	}
	defer reader.Close()
	pkg, err := openGamePackage(reader)
	if err != nil {
		return check, err
	}
	for _, file := range pkg.files {
		name := strings.ToLower(file.name)
		if (!strings.HasSuffix(name, ".nca") && !strings.HasSuffix(name, ".ncz")) || file.size < 0xC00 {
			continue
		}
		// an NCZ keeps the start of its NCA as it was, its header included
		encrypted := make([]byte, 0xC00)
		if err := readAtFull(reader, encrypted, file.offset); err != nil {
			return check, err
		}
		header, err := DecryptNcaHeader(keys.GetKey("header_key"), encrypted)
		if err != nil {
			return check, err
		}
		check.Checked++
		switch signed, known := headerSigned(header.headerBytes); {
		case !known:
			check.Unknown = append(check.Unknown, file.name)
		case !signed:
			check.Unsigned = append(check.Unsigned, file.name)
		}
	}
	return check, nil
}

// headerSigned tells whether a decrypted NCA header has the signature of Nintendo, and whether
// the key it names is known.
func headerSigned(header []byte) (signed bool, known bool) {
	if len(header) < 0x400 {
		return false, true
	}
	generation := int(header[0x221])
	if generation >= len(ncaHeaderModuli) {
		return false, false
	}
	modulus, err := hex.DecodeString(ncaHeaderModuli[generation])
	if err != nil {
		return false, false
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: 65537}
	digest := sha256.Sum256(header[0x200:0x400])
	return rsa.VerifyPSS(key, crypto.SHA256, digest[:], header[:0x100], &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthAuto}) == nil, true
}
