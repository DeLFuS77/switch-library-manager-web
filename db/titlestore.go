package db

import (
	"bytes"
	"compress/flate"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The titles database is a JSON file of over 100 MB. TitleStore keeps a processed copy of
// it, so the app starts without reading the JSON again, and the descriptions and screenshots,
// which only the game page needs, so they do not take memory.

const (
	TITLES_STORE_FILENAME = "titles.db"
	storeInfoBucket       = "info"
	storeCoreKey          = "core"
	storeDetailsBucket    = "details"
	// bump when the stored structures change
	storeFormat = "2"
)

// values are stored compressed: descriptions and the processed titles are text that
// shrinks to a fraction
func compress(data []byte) []byte {
	var buffer bytes.Buffer
	writer := flateWriters.Get().(*flate.Writer)
	writer.Reset(&buffer)
	writer.Write(data)
	writer.Close()
	flateWriters.Put(writer)
	return buffer.Bytes()
}

// a compressor allocates about a megabyte; thousands of values reuse a few
var flateWriters = sync.Pool{New: func() any {
	writer, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
	return writer
}}

func decompress(data []byte) ([]byte, error) {
	return io.ReadAll(flate.NewReader(bytes.NewReader(data)))
}

// TitleDetails are the parts of a title only shown on its page.
type TitleDetails struct {
	Description string   `json:"d,omitempty"`
	Screenshots []string `json:"s,omitempty"`
}

type TitleStore struct {
	db *bolt.DB
}

func OpenTitleStore(dataFolder string) (*TitleStore, error) {
	store, err := bolt.Open(filepath.Join(dataFolder, TITLES_STORE_FILENAME), 0600, &bolt.Options{Timeout: 10 * time.Second})
	if err != nil {
		return nil, err
	}
	return &TitleStore{db: store}, nil
}

func (s *TitleStore) Close() {
	s.db.Close()
}

// FileStamp identifies the content of files by their names, sizes and modification times.
func FileStamp(paths ...string) string {
	parts := []string{storeFormat}
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			return ""
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", filepath.Base(path), info.Size(), info.ModTime().UnixNano()))
	}
	return strings.Join(parts, "|")
}

func (s *TitleStore) stamp(name string) string {
	stamp := ""
	s.db.View(func(tx *bolt.Tx) error {
		if bucket := tx.Bucket([]byte(storeInfoBucket)); bucket != nil {
			stamp = string(bucket.Get([]byte(name)))
		}
		return nil
	})
	return stamp
}

// LoadTitles returns the stored titles when they were made from the files with stamp.
func (s *TitleStore) LoadTitles(stamp string) (*SwitchTitlesDB, bool) {
	if stamp == "" || s.stamp(storeCoreKey) != stamp {
		return nil, false
	}
	var titles map[string]*SwitchTitle
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(storeInfoBucket))
		if bucket == nil {
			return bolt.ErrBucketNotFound
		}
		data, err := decompress(bucket.Get([]byte(storeCoreKey + "-data")))
		if err != nil {
			return err
		}
		return gob.NewDecoder(bytes.NewReader(data)).Decode(&titles)
	})
	if err != nil || titles == nil {
		return nil, false
	}
	return &SwitchTitlesDB{TitlesMap: titles}, true
}

// SaveTitles stores the titles (without their details) and the details, made from the
// files with stamp.
func (s *TitleStore) SaveTitles(stamp string, switchDB *SwitchTitlesDB, details map[string]TitleDetails) error {
	var data bytes.Buffer
	if err := gob.NewEncoder(&data).Encode(switchDB.TitlesMap); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := replaceBucket(tx, storeDetailsBucket, details); err != nil {
			return err
		}
		info, err := tx.CreateBucketIfNotExists([]byte(storeInfoBucket))
		if err != nil {
			return err
		}
		if err := info.Put([]byte(storeCoreKey+"-data"), compress(data.Bytes())); err != nil {
			return err
		}
		return info.Put([]byte(storeCoreKey), []byte(stamp))
	})
}

// Details returns the description and screenshots of a title.
func (s *TitleStore) Details(titleId string) TitleDetails {
	details := TitleDetails{}
	s.db.View(func(tx *bolt.Tx) error {
		if bucket := tx.Bucket([]byte(storeDetailsBucket)); bucket != nil {
			if value := bucket.Get([]byte(strings.ToUpper(titleId))); value != nil {
				if data, err := decompress(value); err == nil {
					json.Unmarshal(data, &details)
				}
			}
		}
		return nil
	})
	return details
}

// LoadLocalized returns the stored names of a language made from the file with stamp.
func (s *TitleStore) LoadLocalized(lang string, stamp string) (map[string]LocalizedTitle, bool) {
	if stamp == "" || s.stamp("localized-"+lang) != stamp {
		return nil, false
	}
	var titles map[string]LocalizedTitle
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte(storeInfoBucket))
		if bucket == nil {
			return bolt.ErrBucketNotFound
		}
		data, err := decompress(bucket.Get([]byte("localized-" + lang + "-data")))
		if err != nil {
			return err
		}
		return gob.NewDecoder(bytes.NewReader(data)).Decode(&titles)
	})
	return titles, err == nil && titles != nil
}

// SaveLocalized stores the names of a language and, apart, their descriptions.
func (s *TitleStore) SaveLocalized(lang string, stamp string, titles map[string]LocalizedTitle, descriptions map[string]TitleDetails) error {
	var data bytes.Buffer
	if err := gob.NewEncoder(&data).Encode(titles); err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := replaceBucket(tx, storeDetailsBucket+"-"+lang, descriptions); err != nil {
			return err
		}
		info, err := tx.CreateBucketIfNotExists([]byte(storeInfoBucket))
		if err != nil {
			return err
		}
		if err := info.Put([]byte("localized-"+lang+"-data"), compress(data.Bytes())); err != nil {
			return err
		}
		return info.Put([]byte("localized-"+lang), []byte(stamp))
	})
}

// LocalizedDescription returns the description of a title in a language, if stored.
func (s *TitleStore) LocalizedDescription(lang string, titleId string) string {
	details := TitleDetails{}
	s.db.View(func(tx *bolt.Tx) error {
		if bucket := tx.Bucket([]byte(storeDetailsBucket + "-" + lang)); bucket != nil {
			if value := bucket.Get([]byte(strings.ToUpper(titleId))); value != nil {
				if data, err := decompress(value); err == nil {
					json.Unmarshal(data, &details)
				}
			}
		}
		return nil
	})
	return details.Description
}

func replaceBucket(tx *bolt.Tx, name string, values map[string]TitleDetails) error {
	if err := tx.DeleteBucket([]byte(name)); err != nil && err != bolt.ErrBucketNotFound {
		return err
	}
	bucket, err := tx.CreateBucket([]byte(name))
	if err != nil {
		return err
	}
	// keys added in order fill the pages completely, which keeps the file small
	bucket.FillPercent = 1
	ids := make([]string, 0, len(values))
	for id, details := range values {
		if details.Description != "" || len(details.Screenshots) > 0 {
			ids = append(ids, strings.ToUpper(id))
		}
	}
	sort.Strings(ids)
	upper := make(map[string]TitleDetails, len(values))
	for id, details := range values {
		upper[strings.ToUpper(id)] = details
	}
	for _, id := range ids {
		value, err := json.Marshal(upper[id])
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte(id), compress(value)); err != nil {
			return err
		}
	}
	return nil
}

// SplitDetails takes the descriptions and screenshots out of the titles, which then use
// much less memory, and returns them by title ID.
func SplitDetails(switchDB *SwitchTitlesDB) map[string]TitleDetails {
	details := make(map[string]TitleDetails, len(switchDB.TitlesMap))
	for _, title := range switchDB.TitlesMap {
		if title.Attributes.Description != "" || len(title.Attributes.Screenshots) > 0 {
			details[strings.ToUpper(title.Attributes.Id)] = TitleDetails{Description: title.Attributes.Description, Screenshots: title.Attributes.Screenshots}
			title.Attributes.Description = ""
			title.Attributes.Screenshots = nil
		}
	}
	return details
}

// SplitLocalizedDescriptions takes the descriptions out of the names of a language.
func SplitLocalizedDescriptions(titles map[string]LocalizedTitle) map[string]TitleDetails {
	descriptions := make(map[string]TitleDetails, len(titles))
	for id, title := range titles {
		if title.Description != "" {
			descriptions[id] = TitleDetails{Description: title.Description}
			title.Description = ""
			titles[id] = title
		}
	}
	return descriptions
}
