package db

import (
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"sync/atomic"

	"github.com/dtrunk90/switch-library-manager-web/fileio"
	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
	"go.uber.org/zap"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	versionRegex = regexp.MustCompile(`\[[vV]?(?P<version>[0-9]{1,10})]`)
	titleIdRegex = regexp.MustCompile(`\[(?P<titleId>[A-Fa-f0-9]{16})]`)
)

const (
	DB_TABLE_FILE_SCAN_METADATA = "deep-scan"
	DB_TABLE_LOCAL_LIBRARY      = "local-library"

	REASON_UNSUPPORTED_TYPE = iota
	REASON_DUPLICATE
	REASON_OLD_UPDATE
	REASON_UNRECOGNISED
	REASON_MALFORMED_FILE
	REASON_FILENAME_FALLBACK
)

const (
	// bump when the structure or keys of the cached local library change,
	// so caches written by older versions are rebuilt instead of misread
	LIBRARY_SCHEMA_VERSION = "2"
	DB_KEY_LIBRARY_SCHEMA  = "library_schema"
	// fingerprint of the keys the cached library was scanned with
	DB_KEY_LIBRARY_KEYS = "library_keys"
)

type LocalSwitchDBManager struct {
	db *PersistentDB
}

func NewLocalSwitchDBManager(dataFolder string) (*LocalSwitchDBManager, error) {
	db, err := NewPersistentDB(dataFolder)
	if err != nil {
		return nil, err
	}

	if db.GetInternalValue(DB_KEY_LIBRARY_SCHEMA) != LIBRARY_SCHEMA_VERSION {
		zap.S().Info("Local library cache was created by an older version, it will be rebuilt")
		if err := db.ClearTable(DB_TABLE_LOCAL_LIBRARY); err != nil {
			db.Close()
			return nil, err
		}
		if err := db.SetInternalValue(DB_KEY_LIBRARY_SCHEMA, LIBRARY_SCHEMA_VERSION); err != nil {
			db.Close()
			return nil, err
		}
	}

	return &LocalSwitchDBManager{db: db}, nil
}

// KeysChanged reports whether the cached library was scanned with other keys than
// fingerprint, and records fingerprint for the next check.
func (ldb *LocalSwitchDBManager) KeysChanged(fingerprint string) bool {
	changed := ldb.db.GetInternalValue(DB_KEY_LIBRARY_KEYS) != fingerprint
	if changed {
		if err := ldb.db.SetInternalValue(DB_KEY_LIBRARY_KEYS, fingerprint); err != nil {
			zap.S().Warnf("Failed to save the keys fingerprint: %v", err)
		}
	}
	return changed
}

func (ldb *LocalSwitchDBManager) Close() {
	ldb.db.Close()
}

type ExtendedFileInfo struct {
	FileName   string
	BaseFolder string
	Size       int64
	IsDir      bool
}

type SwitchFileInfo struct {
	ExtendedInfo ExtendedFileInfo
	Metadata     *switchfs.ContentMetaAttributes
}

type SwitchGameFiles struct {
	File         SwitchFileInfo
	BaseExist    bool
	Updates      map[int]SwitchFileInfo
	Dlc          map[string]SwitchFileInfo
	MultiContent bool
	LatestUpdate int
	IsSplit      bool
	Icon         string
	Banner       string
}

type SkippedFile struct {
	ReasonCode     int
	ReasonText     string
	AdditionalInfo string
}

type LocalSwitchFilesDB struct {
	TitlesMap map[string]*SwitchGameFiles
	Skipped   map[ExtendedFileInfo]SkippedFile
	NumFiles  int
}

func (ldb *LocalSwitchDBManager) CreateLocalSwitchFilesDB(switchDB *SwitchTitlesDB, dataFolder string,
	folders []string, progress ProgressUpdater, recursive bool, ignoreCache bool) (*LocalSwitchFilesDB, error) {

	titles := map[string]*SwitchGameFiles{}
	skipped := map[ExtendedFileInfo]SkippedFile{}
	files := []ExtendedFileInfo{}

	if !ignoreCache {
		err := ldb.db.GetEntry(DB_TABLE_LOCAL_LIBRARY, "files", &files)
		if err == nil {
			err = ldb.db.GetEntry(DB_TABLE_LOCAL_LIBRARY, "skipped", &skipped)
		}
		if err == nil {
			err = ldb.db.GetEntry(DB_TABLE_LOCAL_LIBRARY, "titles", &titles)
		}
		if err != nil {
			zap.S().Warnf("Failed to read local library cache, rescanning - %v", err)
			titles = map[string]*SwitchGameFiles{}
			skipped = map[ExtendedFileInfo]SkippedFile{}
			files = []ExtendedFileInfo{}
		}
	}

	if len(titles) == 0 {
		// start from scratch, a cache without titles must not add its files a second time
		files = []ExtendedFileInfo{}
		skipped = map[ExtendedFileInfo]SkippedFile{}

		for _, folder := range folders {
			err := scanFolder(folder, recursive, &files, progress)
			if progress != nil {
				progress.UpdateProgress(-1, -1, "Scanned "+folder)
			}
			if err != nil {
				zap.S().Errorf("Failed to scan folder %v - %v", folder, err)
				continue
			}
		}

		ldb.processLocalFiles(switchDB, dataFolder, files, progress, titles, skipped)

		if err := ldb.db.AddEntries(DB_TABLE_LOCAL_LIBRARY, map[string]interface{}{"files": files, "skipped": skipped, "titles": titles}); err != nil {
			return nil, err
		}
	}

	if progress != nil {
		progress.UpdateProgress(len(files), len(files), "Complete")
	}

	return &LocalSwitchFilesDB{TitlesMap: titles, Skipped: skipped, NumFiles: len(files)}, nil
}

func scanFolder(folder string, recursive bool, files *[]ExtendedFileInfo, progress ProgressUpdater) error {
	if _, err := os.Stat(folder); err != nil {
		return err
	}
	// WalkDir reads the entry types from the directory listing; only files are stat'ed
	return filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if path == folder {
			return nil
		}
		if err != nil {
			zap.S().Error("Error while scanning folders", err)
			return nil
		}

		if entry.IsDir() {
			return nil
		}

		//skip hidden files (including macOS "._" resource forks)
		if strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			// removed while scanning
			return nil
		}
		base := path[0 : len(path)-len(info.Name())]
		if strings.TrimSuffix(base, string(os.PathSeparator)) != strings.TrimSuffix(folder, string(os.PathSeparator)) &&
			!recursive {
			return nil
		}
		if progress != nil {
			progress.UpdateProgress(-1, -1, "Found "+info.Name())
		}
		*files = append(*files, ExtendedFileInfo{FileName: info.Name(), BaseFolder: base, Size: info.Size(), IsDir: info.IsDir()})

		return nil
	})
}

func (ldb *LocalSwitchDBManager) ClearScanData() error {
	return ldb.db.ClearTable(DB_TABLE_FILE_SCAN_METADATA)
}

func (ldb *LocalSwitchDBManager) processLocalFiles(switchDB *SwitchTitlesDB, dataFolder string,
	files []ExtendedFileInfo,
	progress ProgressUpdater,
	titles map[string]*SwitchGameFiles,
	skipped map[ExtendedFileInfo]SkippedFile) {

	// unsupported files with these extensions are not reported as issues
	ignoreFileTypes := map[string]struct{}{}
	for _, ext := range settings.ReadSettings(dataFolder).IgnoreFileTypes {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext != "" {
			ignoreFileTypes["."+strings.TrimPrefix(ext, ".")] = struct{}{}
		}
	}

	// pick the game files; the others are only reported
	candidates := []int{}
	isSplitFile := make([]bool, len(files))
	for i, file := range files {
		if file.IsDir {
			continue
		}

		fileName := strings.ToLower(file.FileName)
		isSplit := false

		if len(fileName) >= 2 {
			if partNum, err := strconv.Atoi(fileName[len(fileName)-2:]); err == nil {
				if partNum == 0 {
					isSplit = true
				} else {
					continue
				}
			}
		}

		//only handle NSZ and NSP files

		if !isSplit &&
			!strings.HasSuffix(fileName, "xci") &&
			!strings.HasSuffix(fileName, "nsp") &&
			!strings.HasSuffix(fileName, "nsz") &&
			!strings.HasSuffix(fileName, "xcz") {
			if _, ok := ignoreFileTypes[filepath.Ext(fileName)]; !ok {
				skipped[file] = SkippedFile{ReasonCode: REASON_UNSUPPORTED_TYPE, ReasonText: "file type is not supported"}
			}
			continue
		}
		isSplitFile[i] = isSplit
		candidates = append(candidates, i)
	}

	// read the files in parallel, with a bounded number of workers so a big library does
	// not saturate the disk, the network share or the CPU
	results := make([]metadataResult, len(files))
	total := len(candidates)
	var read atomic.Int64
	jobs := make(chan int)
	var workers sync.WaitGroup
	for w := 0; w < ScanWorkers(dataFolder); w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i] = ldb.readGameMetadata(files[i])
				if progress != nil {
					progress.UpdateProgress(int(read.Add(1)), total, "Reading "+files[i].FileName)
				}
			}
		}()
	}
	for _, i := range candidates {
		jobs <- i
	}
	close(jobs)
	workers.Wait()

	// newly read metadata is cached in one transaction, not one per file
	newEntries := map[string]interface{}{}
	for _, i := range candidates {
		if results[i].cacheKey != "" {
			newEntries[results[i].cacheKey] = results[i].metadata
		}
	}
	if len(newEntries) > 0 {
		if err := ldb.db.AddEntries(DB_TABLE_FILE_SCAN_METADATA, newEntries); err != nil {
			zap.S().Warnf("Failed to cache the metadata of %v files: %v", len(newEntries), err)
		}
	}

	covers := []coverDownload{}

	// combine the results in the order of the files, so duplicates and old versions are
	// decided like in a sequential scan
	for _, i := range candidates {
		file := files[i]
		isSplit := isSplitFile[i]
		result := results[i]
		if result.skip != nil {
			skipped[file] = *result.skip
		}
		contentMap, err := result.metadata, result.err

		if err != nil {
			if _, ok := skipped[file]; !ok {
				skipped[file] = SkippedFile{ReasonText: "unable to determine title-Id / version - " + err.Error(), ReasonCode: REASON_UNRECOGNISED}
			}
			continue
		}

		for _, metadata := range contentMap {

			id := strings.ToLower(metadata.TitleId)
			idPrefix, prefixErr := titleIDPrefix(id)
			if prefixErr != nil {
				skipped[file] = SkippedFile{ReasonText: "unable to determine title-Id / version - " + prefixErr.Error(), ReasonCode: REASON_UNRECOGNISED}
				continue
			}
			metadata.TitleId = id

			multiContent := len(contentMap) > 1
			switchTitle := &SwitchGameFiles{
				MultiContent: multiContent,
				Updates:      map[int]SwitchFileInfo{},
				Dlc:          map[string]SwitchFileInfo{},
				BaseExist:    false,
				IsSplit:      isSplit,
				LatestUpdate: 0,
			}
			if t, ok := titles[idPrefix]; ok {
				switchTitle = t
			}
			titles[idPrefix] = switchTitle

			//process Updates
			if strings.HasSuffix(metadata.TitleId, "800") {
				metadata.Type = "Update"

				if update, ok := switchTitle.Updates[metadata.Version]; ok {
					skipped[file] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate update file (" + fullPath(update.ExtendedInfo) + ")"}
					zap.S().Warnf("-->Duplicate update file found [%v] and [%v]", update.ExtendedInfo.FileName, file.FileName)
					continue
				}
				switchTitle.Updates[metadata.Version] = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
				if metadata.Version > switchTitle.LatestUpdate {
					if switchTitle.LatestUpdate != 0 {
						skipped[switchTitle.Updates[switchTitle.LatestUpdate].ExtendedInfo] = SkippedFile{ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + fullPath(file) + ")"}
					}
					switchTitle.LatestUpdate = metadata.Version
				} else {
					skipped[file] = SkippedFile{ReasonCode: REASON_OLD_UPDATE, ReasonText: "old update file, newer update exist locally (" + fullPath(switchTitle.Updates[switchTitle.LatestUpdate].ExtendedInfo) + ")"}
				}
				continue
			}

			//process base
			if strings.HasSuffix(metadata.TitleId, "000") {
				metadata.Type = "Base"
				if switchTitle.BaseExist {
					skipped[file] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate base file (" + fullPath(switchTitle.File.ExtendedInfo) + ")"}
					zap.S().Warnf("-->Duplicate base file found [%v] and [%v]", file.FileName, switchTitle.File.ExtendedInfo.FileName)
					continue
				}
				switchTitle.File = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
				switchTitle.BaseExist = true

				if switchDB == nil {
					continue
				}

				if title, ok := switchDB.TitlesMap[idPrefix]; ok {
					if title.Attributes.IconUrl != "" {
						covers = append(covers, coverDownload{title: switchTitle, url: title.Attributes.IconUrl, icon: true})
					}
					if title.Attributes.BannerUrl != "" {
						covers = append(covers, coverDownload{title: switchTitle, url: title.Attributes.BannerUrl})
					}
				}

				continue
			}

			if dlc, ok := switchTitle.Dlc[metadata.TitleId]; ok {
				if metadata.Version < dlc.Metadata.Version {
					skipped[file] = SkippedFile{ReasonCode: REASON_OLD_UPDATE, ReasonText: "old DLC file, newer version exist locally (" + fullPath(dlc.ExtendedInfo) + ")"}
					zap.S().Warnf("-->Old DLC file found [%v] and [%v]", file.FileName, dlc.ExtendedInfo.FileName)
					continue
				} else if metadata.Version == dlc.Metadata.Version {
					skipped[file] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + fullPath(dlc.ExtendedInfo) + ")"}
					zap.S().Warnf("-->Duplicate DLC file found [%v] and [%v]", file.FileName, dlc.ExtendedInfo.FileName)
					continue
				}
			}
			//not an update, and not main TitleAttributes, so treat it as a DLC
			metadata.Type = "DLC"
			switchTitle.Dlc[metadata.TitleId] = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
		}
	}

	downloadCovers(dataFolder, covers, progress)
}

// metadataResult is what reading one file found. Workers fill it in parallel; it is applied
// to the library afterwards, in the order of the files.
type metadataResult struct {
	metadata map[string]*switchfs.ContentMetaAttributes
	// why the file is listed as an issue, even if it was identified
	skip *SkippedFile
	err  error
	// set when the metadata was read from the file and should be cached under this key
	cacheKey string
}

// readGameMetadata identifies a file: from the cache, by reading it with the keys, or
// from its name. It is safe to call from several goroutines.
func (ldb *LocalSwitchDBManager) readGameMetadata(file ExtendedFileInfo) metadataResult {
	result := metadataResult{}
	filePath := filepath.Join(file.BaseFolder, file.FileName)
	keys, _ := settings.SwitchKeys()
	fileKey := filePath + "|" + file.FileName + "|" + strconv.Itoa(int(file.Size))

	if keys != nil && keys.GetKey("header_key") != "" {
		var metadata map[string]*switchfs.ContentMetaAttributes
		if err := ldb.db.GetEntry(DB_TABLE_FILE_SCAN_METADATA, fileKey, &metadata); err != nil {
			zap.S().Warnf("%v", err)
		}
		if metadata != nil {
			result.metadata = metadata
			return result
		}

		var err error
		fileName := strings.ToLower(file.FileName)
		kind := ""
		if strings.HasSuffix(fileName, "nsp") ||
			strings.HasSuffix(fileName, "nsz") {
			kind = "NSP"
			metadata, err = switchfs.ReadNspMetadata(filePath)
		} else if strings.HasSuffix(fileName, "xci") ||
			strings.HasSuffix(fileName, "xcz") {
			kind = "XCI"
			metadata, err = switchfs.ReadXciMetadata(filePath)
		} else if strings.HasSuffix(fileName, "00") {
			kind = "split files"
			metadata, err = fileio.ReadSplitFileMetadata(filePath)
		}
		if err == nil && len(metadata) == 0 && kind != "" {
			// a file without content metadata cannot be identified: report it
			metadata = nil
			err = errors.New("no content metadata found")
		}
		if err != nil {
			result.skip = &SkippedFile{ReasonCode: REASON_MALFORMED_FILE, ReasonText: readErrorText(kind, err)}
			zap.S().Warnf("[file:%v] failed to read %v [reason: %v]", file.FileName, kind, err)
		}
		if metadata != nil {
			result.metadata = metadata
			result.cacheKey = fileKey
			return result
		}
	}

	//fallback to parse data from filename
	titleId, _ := parseTitleIdFromFileName(file.FileName)
	version, _ := parseVersionFromFileName(file.FileName)

	if titleId == nil || version == nil {
		result.err = errors.New("unable to determine titleId / version")
		return result
	}
	result.metadata = map[string]*switchfs.ContentMetaAttributes{*titleId: {TitleId: *titleId, Version: *version}}

	// the file is still listed in the library, so explain why it also appears as an issue
	if result.skip != nil && result.skip.ReasonCode == REASON_MALFORMED_FILE {
		result.skip.ReasonCode = REASON_FILENAME_FALLBACK
		result.skip.ReasonText = "identified by file name only, " + result.skip.ReasonText
	}

	return result
}

func parseVersionFromFileName(fileName string) (*int, error) {
	res := versionRegex.FindStringSubmatch(fileName)
	if len(res) != 2 {
		return nil, errors.New("failed to parse name - no version id found")
	}
	ver, err := strconv.Atoi(res[1])
	if err != nil {
		return nil, errors.New("failed to parse name - no version id found")
	}
	return &ver, nil
}

func parseTitleIdFromFileName(fileName string) (*string, error) {
	res := titleIdRegex.FindStringSubmatch(fileName)

	if len(res) != 2 {
		return nil, errors.New("failed to parse name - no title id found")
	}
	titleId := strings.ToLower(res[1])
	return &titleId, nil
}

func ParseTitleNameFromFileName(fileName string) string {
	ind := strings.Index(fileName, "[")
	if ind != -1 {
		return fileName[:ind]
	}
	return fileName
}

func fullPath(file ExtendedFileInfo) string {
	return filepath.Join(file.BaseFolder, file.FileName)
}

// readErrorText explains why a file could not be read, with a hint for missing keys.
func readErrorText(kind string, err error) string {
	var missingKey *switchfs.MissingKeyError
	if errors.As(err, &missingKey) {
		return fmt.Sprintf("failed to read %v: prod.keys has no %v. The title needs keys from a newer firmware: "+
			"update prod.keys, or add [TitleID][vVersion] to the file name", kind, missingKey.KeyName)
	}
	return fmt.Sprintf("failed to read %v [reason: %v]", kind, err)
}
