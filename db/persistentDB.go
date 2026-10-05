package db

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	bolt "go.etcd.io/bbolt"
	"go.uber.org/zap"
)

const (
	DB_INTERNAL_TABLENAME = "internal-metadata"
)

type PersistentDB struct {
	db *bolt.DB
}

func NewPersistentDB(baseFolder string) (*PersistentDB, error) {
	// Open the my.db data file in your current directory.
	// It will be created if it doesn't exist.
	db, err := bolt.Open(filepath.Join(baseFolder, "slm.db"), 0600, &bolt.Options{Timeout: time.Minute})
	if err != nil {
		return nil, err
	}

	//set DB version
	err = db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(DB_INTERNAL_TABLENAME))
		if b == nil {
			b, err := tx.CreateBucket([]byte(DB_INTERNAL_TABLENAME))
			if b == nil || err != nil {
				return fmt.Errorf("create bucket: %s", err)
			}
			err = b.Put([]byte("app_version"), []byte(settings.SLM_VERSION))
			if err != nil {
				zap.S().Warnf("failed to save app_version - %v", err)
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}

	return &PersistentDB{db: db}, nil
}

func (pd *PersistentDB) Close() {
	pd.db.Close()
}

func (pd *PersistentDB) ClearTable(tableName string) error {
	err := pd.db.Update(func(tx *bolt.Tx) error {
		err := tx.DeleteBucket([]byte(tableName))
		if err == bolt.ErrBucketNotFound {
			return nil
		}
		return err
	})
	return err
}

func (pd *PersistentDB) AddEntry(tableName string, key string, value interface{}) error {
	var err error
	err = pd.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(tableName))
		if b == nil {
			b, err = tx.CreateBucket([]byte(tableName))
			if b == nil || err != nil {
				return fmt.Errorf("create bucket: %s", err)
			}
		}
		var bytesBuff bytes.Buffer
		encoder := gob.NewEncoder(&bytesBuff)
		err := encoder.Encode(value)
		if err != nil {
			return err
		}
		err = b.Put([]byte(key), bytesBuff.Bytes())
		return err
	})
	return err
}

// AddEntries stores several values in one transaction, which is much faster than one
// transaction per value: every transaction waits for the disk.
func (pd *PersistentDB) AddEntries(tableName string, values map[string]interface{}) error {
	return pd.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(tableName))
		if err != nil {
			return fmt.Errorf("create bucket: %s", err)
		}
		for key, value := range values {
			var bytesBuff bytes.Buffer
			if err := gob.NewEncoder(&bytesBuff).Encode(value); err != nil {
				return err
			}
			if err := b.Put([]byte(key), bytesBuff.Bytes()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (pd *PersistentDB) GetEntry(tableName string, key string, value interface{}) error {
	err := pd.db.View(func(tx *bolt.Tx) error {

		b := tx.Bucket([]byte(tableName))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(key))
		if v == nil {
			return nil
		}
		d := gob.NewDecoder(bytes.NewReader(v))

		// Decoding the serialized data
		err := d.Decode(value)
		if err != nil {
			return err
		}
		return nil
	})
	return err
}

/*func (pd *PersistentDB) GetEntries() (map[string]*switchfs.ContentMetaAttributes, error) {
	pd.db.View(func(tx *bolt.Tx) error {
		// Assume bucket exists and has keys
		b := tx.Bucket([]byte(METADATA_TABLENAME))

		c := b.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			fmt.Printf("key=%s, value=%s\n", k, v)
		}

		return nil
	})
}*/

// GetInternalValue returns a value stored in the internal metadata table, or "" if missing.
func (pd *PersistentDB) GetInternalValue(key string) string {
	var value string
	_ = pd.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(DB_INTERNAL_TABLENAME)); b != nil {
			value = string(b.Get([]byte(key)))
		}
		return nil
	})
	return value
}

// SetInternalValue stores a value in the internal metadata table.
func (pd *PersistentDB) SetInternalValue(key string, value string) error {
	return pd.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(DB_INTERNAL_TABLENAME))
		if err != nil {
			return err
		}
		return b.Put([]byte(key), []byte(value))
	})
}
