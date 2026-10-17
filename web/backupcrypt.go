package web

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/argon2"
)

// An encrypted backup is the ZIP file sealed with AES-256-GCM, with a key derived from its
// password by Argon2id: the magic, the salt, the nonce, then the sealed ZIP. The backup holds
// the password hashes of the users and the tokens of the notifications, so one kept outside
// the server (a cloud folder, an e-mail) can be locked with a password.
var encryptedBackupMagic = []byte("SLMBACKUP1")

const (
	backupSaltSize = 16
	// Argon2id: 3 passes over 64 MB, so each guess of the password is slow
	backupKdfTime    = 3
	backupKdfMemory  = 64 * 1024
	backupKdfThreads = 4
	// a password shorter than this would be guessed quickly from a stolen file
	minBackupPasswordLength = 10
)

var (
	ErrBackupPasswordNeeded = errors.New("This backup is encrypted: enter its password.")
	ErrBackupPasswordWrong  = errors.New("The password of the backup is not right.")
	ErrBackupPasswordShort  = errors.New("The password of the backup needs at least 10 characters.")
)

func backupKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, backupKdfTime, backupKdfMemory, backupKdfThreads, 32)
}

// encryptBackup seals a backup with a password.
func encryptBackup(data []byte, password string) ([]byte, error) {
	if len([]rune(password)) < minBackupPasswordLength {
		return nil, ErrBackupPasswordShort
	}
	salt := make([]byte, backupSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	gcm, err := backupCipher(password, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	header := append(append(append([]byte{}, encryptedBackupMagic...), salt...), nonce...)
	// the header is authenticated too: a changed salt or nonce is a wrong password
	return gcm.Seal(header, nonce, data, header), nil
}

// isEncryptedBackup tells a backup sealed with a password.
func isEncryptedBackup(data []byte) bool {
	return bytes.HasPrefix(data, encryptedBackupMagic)
}

// decryptBackup opens a backup sealed with a password; a backup that is not sealed is
// returned as it is.
func decryptBackup(data []byte, password string) ([]byte, error) {
	if !isEncryptedBackup(data) {
		return data, nil
	}
	if password == "" {
		return nil, ErrBackupPasswordNeeded
	}
	headerSize := len(encryptedBackupMagic) + backupSaltSize
	if len(data) < headerSize {
		return nil, ErrBackupInvalid
	}
	salt := data[len(encryptedBackupMagic):headerSize]
	gcm, err := backupCipher(password, salt)
	if err != nil {
		return nil, err
	}
	if len(data) < headerSize+gcm.NonceSize()+gcm.Overhead() {
		return nil, ErrBackupInvalid
	}
	header := data[:headerSize+gcm.NonceSize()]
	nonce := header[headerSize:]
	plain, err := gcm.Open(nil, nonce, data[len(header):], header)
	if err != nil {
		return nil, ErrBackupPasswordWrong
	}
	return plain, nil
}

func backupCipher(password string, salt []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(backupKey(password, salt))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
