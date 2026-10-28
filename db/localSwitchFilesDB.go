package db

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

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
	// the titles database of the scan running, to tell the games it does not know
	titles *SwitchTitlesDB
	// the files the last scan found, for the progress of the next one
	lastFileCount int
	// the temporary files of the app the last walk of the folders found
	leftovers []LeftoverFile
	// the folders the last walk found with nothing in them but files of the system
	emptyFolders []string
	// CheckedPartly tells the copies the last check of the files could not check whole (a tool
	// rewrote their NCA files, and some sections cannot be checked on their own): a copy
	// checked whole is kept instead. Nil when nothing is known of the checks.
	CheckedPartly func(path string) bool
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

const (
	// files read before the found games are added to the library
	scanChunkSize = 1000
)

// how often a slow scan shows the games found so far; a variable for tests
var partialLibraryInterval = 5 * time.Second

// PartialLibraryReceiver is implemented by progress receivers that show the games of a
// slow scan before it ends. The maps are copies the receiver may keep.
type PartialLibraryReceiver interface {
	PartialLibrary(titles map[string]*SwitchGameFiles, skipped map[ExtendedFileInfo]SkippedFile, files int)
}

// copyTitles copies the games of a library that is still being built, so the copy does
// not change while the scan goes on.
func copyTitles(titles map[string]*SwitchGameFiles) map[string]*SwitchGameFiles {
	result := make(map[string]*SwitchGameFiles, len(titles))
	for prefix, title := range titles {
		copied := *title
		copied.Updates = make(map[int]SwitchFileInfo, len(title.Updates))
		for version, update := range title.Updates {
			copied.Updates[version] = update
		}
		copied.Dlc = make(map[string]SwitchFileInfo, len(title.Dlc))
		for id, dlc := range title.Dlc {
			copied.Dlc[id] = dlc
		}
		result[prefix] = &copied
	}
	return result
}

func copySkipped(skipped map[ExtendedFileInfo]SkippedFile) map[ExtendedFileInfo]SkippedFile {
	result := make(map[ExtendedFileInfo]SkippedFile, len(skipped))
	for file, reason := range skipped {
		result[file] = reason
	}
	return result
}

// NonGameFileTypes are the extensions of files that are found next to games and are never
// games: covers and screenshots of other tools, notes, checksums, downloads left halfway by
// a browser or a download client... Archives (zip, rar, 7z) and loose parts of games (nca)
// are not among them: they may hold games, and the Issues page lists them.
var NonGameFileTypes = []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".bmp", ".ico", ".svg", ".txt", ".nfo", ".diz", ".md",
	".rtf", ".pdf", ".csv", ".json", ".xml", ".ini", ".cfg", ".url", ".lnk", ".html", ".htm", ".db", ".log", ".srt", ".m3u",
	".sfv", ".md5", ".sha1", ".sha256", ".sha512", ".crc", ".par2", ".torrent", ".ds_store",
	".part", ".partial", ".tmp", ".bak", ".crdownload", ".download", ".aria2", ".!qb", ".!ut"}

// IsGameFileType reports whether files with this extension (with its dot) are games, which
// can never be ignored.
func IsGameFileType(extension string) bool {
	switch strings.ToLower(extension) {
	case ".nsp", ".nsz", ".xci", ".xcz":
		return true
	}
	return false
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
	// temporary files of the app found by the walk of the folders (see LeftoverFile)
	Leftovers []LeftoverFile
	// folders found by the walk with nothing in them but files Windows or macOS leave
	// (SystemFile): deleted by the cleanup with the folders it empties, when asked
	EmptyFolders []string
}

// LeftoverFile is a hidden temporary file the app writes next to a game before it takes its
// place (a compression, a conversion, a measure of the disk): one left behind by a task the
// app was stopped in the middle of, and that did not go on, takes space without being seen.
type LeftoverFile struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// leftoverName matches the names of the temporary files of the app, and only those:
// ".<game>.nsz.tmp" and the like, and ".slm-measure-*.tmp"
var leftoverName = regexp.MustCompile(`(?i)^\..+\.(nsp|nsz|xci|xcz)\.tmp$|^\.slm-measure-.*\.tmp$`)

func (ldb *LocalSwitchDBManager) CreateLocalSwitchFilesDB(switchDB *SwitchTitlesDB, dataFolder string,
	folders []string, progress ProgressUpdater, recursive bool, ignoreCache bool) (*LocalSwitchFilesDB, error) {

	titles := map[string]*SwitchGameFiles{}
	skipped := map[ExtendedFileInfo]SkippedFile{}
	files := []ExtendedFileInfo{}
	ldb.titles = switchDB

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
		// the folders are walked before the number of files is known: the last scan found
		// about as many, so the progress goes up to that number instead of staying unknown
		estimate := ldb.lastFileCount
		if estimate == 0 {
			var previous []ExtendedFileInfo
			if ldb.db.GetEntry(DB_TABLE_LOCAL_LIBRARY, "files", &previous) == nil {
				estimate = len(previous)
			}
		}

		// start from scratch, a cache without titles must not add its files a second time
		files = []ExtendedFileInfo{}
		skipped = map[ExtendedFileInfo]SkippedFile{}

		// a folder that cannot be reached (a share not mounted, a disk unplugged) fails the
		// scan, so the library is kept: scanned as empty, every game would count as deleted,
		// and as new when the folder is back, for the history, the notifications and the
		// automations
		for _, folder := range folders {
			if info, err := os.Stat(folder); err != nil || !info.IsDir() {
				if err == nil {
					err = errors.New("not a folder")
				}
				return nil, fmt.Errorf("the library folder %s cannot be reached: %w", folder, err)
			}
		}

		ldb.leftovers, ldb.emptyFolders = nil, nil
		for _, folder := range distinctFolders(folders, recursive) {
			err := scanFolder(folder, recursive, &files, &ldb.leftovers, &ldb.emptyFolders, progress, estimate)
			if progress != nil {
				progress.UpdateProgress(-1, -1, "Scanned "+folder)
			}
			if err != nil {
				zap.S().Errorf("Failed to scan folder %v - %v", folder, err)
				continue
			}
		}

		files = distinctFiles(files)
		ldb.processLocalFiles(switchDB, dataFolder, files, progress, titles, skipped)

		if err := ldb.db.AddEntries(DB_TABLE_LOCAL_LIBRARY, map[string]interface{}{"files": files, "skipped": skipped, "titles": titles}); err != nil {
			return nil, err
		}
	}

	if progress != nil {
		progress.UpdateProgress(len(files), len(files), "Complete")
	}
	ldb.lastFileCount = len(files)

	return &LocalSwitchFilesDB{TitlesMap: titles, Skipped: skipped, NumFiles: len(files), Leftovers: ldb.leftovers, EmptyFolders: ldb.emptyFolders}, nil
}

// distinctFolders leaves out a folder listed twice and, when the folders are scanned with their
// subfolders, a folder inside another one listed: its files would be read twice, and each one
// would count as a duplicate of itself, offered to be deleted with its only copy.
func distinctFolders(folders []string, recursive bool) []string {
	cleaned := []string{}
	seen := map[string]bool{}
	for _, folder := range folders {
		folder = filepath.Clean(folder)
		key := folderKey(folder)
		if !seen[key] {
			seen[key] = true
			cleaned = append(cleaned, folder)
		}
	}
	if !recursive {
		return cleaned
	}
	distinct := []string{}
	for _, folder := range cleaned {
		inside := false
		for _, other := range cleaned {
			if other == folder {
				continue
			}
			if rel, err := filepath.Rel(folderKey(other), folderKey(folder)); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				inside = true
				break
			}
		}
		if !inside {
			distinct = append(distinct, folder)
		}
	}
	return distinct
}

// folderKey compares folders the way the system does: Windows and macOS ignore the case.
func folderKey(folder string) string {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.ToLower(folder)
	}
	return folder
}

// distinctFiles keeps each file once, by its path.
func distinctFiles(files []ExtendedFileInfo) []ExtendedFileInfo {
	seen := map[string]bool{}
	distinct := files[:0]
	for _, file := range files {
		key := folderKey(filepath.Join(file.BaseFolder, file.FileName))
		if seen[key] {
			continue
		}
		seen[key] = true
		distinct = append(distinct, file)
	}
	return distinct
}

// scanFolder adds the files of a folder; estimate is how many files the folders are thought
// to hold, for the progress, or 0 when unknown.
func scanFolder(folder string, recursive bool, files *[]ExtendedFileInfo, leftovers *[]LeftoverFile, emptyFolders *[]string, progress ProgressUpdater, estimate int) error {
	if _, err := os.Stat(folder); err != nil {
		return err
	}
	// the folders below, and whether something else than files of the system is in them
	held := map[string]bool{}
	folders := []string{}
	// WalkDir reads the entry types from the directory listing; only files are stat'ed
	err := filepath.WalkDir(folder, func(path string, entry fs.DirEntry, err error) error {
		if path == folder {
			return nil
		}
		if err != nil {
			zap.S().Error("Error while scanning folders", err)
			// a folder that cannot be read is never taken for an empty one
			held[path] = true
			held[filepath.Dir(path)] = true
			return nil
		}
		if entry.IsDir() && entry.Name() == QUARANTINE_FOLDER {
			return filepath.SkipDir
		}

		if !SystemFile(entry) {
			held[filepath.Dir(path)] = true
		}
		if entry.IsDir() {
			if _, seen := held[path]; !seen {
				held[path] = false
			}
			folders = append(folders, path)
			return nil
		}

		// skip hidden files (including macOS "._" resource forks and the temporary files of the
		// app), but not a game whose name starts with a dot (".hack//G.U.", named so by an older
		// version or another tool): skipped, it was never in the library nor organized
		if name := entry.Name(); strings.HasPrefix(name, ".") {
			leftover := leftoverName.MatchString(name)
			if leftovers != nil && leftover {
				if info, err := entry.Info(); err == nil {
					*leftovers = append(*leftovers, LeftoverFile{Path: path, Size: info.Size(), ModTime: info.ModTime()})
				}
			}
			if leftover || strings.HasPrefix(name, "._") || !IsGameFileType(filepath.Ext(name)) {
				return nil
			}
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
			if found := len(*files) + 1; estimate > 0 {
				// never full before the walk ends: there may be more files than last time
				progress.UpdateProgress(min(found, estimate*99/100), estimate, "Found "+info.Name())
			} else {
				progress.UpdateProgress(-1, -1, "Found "+info.Name())
			}
		}
		*files = append(*files, ExtendedFileInfo{FileName: info.Name(), BaseFolder: base, Size: info.Size(), IsDir: info.IsDir()})

		return nil
	})
	if emptyFolders != nil && recursive {
		for _, path := range folders {
			if !held[path] {
				*emptyFolders = append(*emptyFolders, path)
			}
		}
	}
	return err
}

// systemFiles are the files Windows and macOS leave in the folders they show: thumbnails,
// settings of the view, and the "._" files of macOS on other disks.
var systemFiles = map[string]bool{"thumbs.db": true, "ehthumbs.db": true, "desktop.ini": true, ".ds_store": true}

// SystemFile tells a file Windows or macOS leaves in a folder it shows, which does not make
// the folder hold anything.
// QUARANTINE_FOLDER holds the files deleted that can still be put back (see the quarantine of
// the web interface): never part of the library.
const QUARANTINE_FOLDER = ".slm-quarantine"

func SystemFile(entry fs.DirEntry) bool {
	name := strings.ToLower(entry.Name())
	return !entry.IsDir() && (systemFiles[name] || strings.HasPrefix(name, "._"))
}

// SaveTitles stores the games of the library again, e.g. after their covers were
// downloaded, so the next start shows them right away.
func (ldb *LocalSwitchDBManager) SaveTitles(titles map[string]*SwitchGameFiles) error {
	return ldb.db.AddEntries(DB_TABLE_LOCAL_LIBRARY, map[string]interface{}{"titles": titles})
}

func (ldb *LocalSwitchDBManager) processLocalFiles(switchDB *SwitchTitlesDB, dataFolder string,
	files []ExtendedFileInfo,
	progress ProgressUpdater,
	titles map[string]*SwitchGameFiles,
	skipped map[ExtendedFileInfo]SkippedFile) {

	// unsupported files with these extensions are not reported as issues: the ones that are
	// never games (images, notes...) and the ones the user chose
	ignoreFileTypes := map[string]struct{}{}
	for _, ext := range NonGameFileTypes {
		ignoreFileTypes[ext] = struct{}{}
	}
	organizeOptions := settings.ReadSettings(dataFolder).OrganizeOptions
	keepDuplicate := organizeOptions.KeepDuplicate
	chosen := map[string]bool{}
	for _, path := range organizeOptions.KeepFiles {
		chosen[filepath.Clean(path)] = true
	}
	// the number of titles of each file read, to tell the files with several titles
	titlesInFile := map[ExtendedFileInfo]int{}
	// flagged tells whether a file is listed as an issue for another reason than being a copy
	// (it was identified by its name only...)
	flagged := func(file ExtendedFileInfo) bool {
		reason, ok := skipped[file]
		return ok && reason.ReasonCode != REASON_DUPLICATE && reason.ReasonCode != REASON_OLD_UPDATE
	}
	partly := func(file ExtendedFileInfo) bool {
		return ldb.CheckedPartly != nil && ldb.CheckedPartly(fullPath(file))
	}
	// keepInstead tells whether file is kept instead of the copy found first, as chosen in
	// the settings; a file with an issue is never preferred to one without, and one without
	// takes the place of one with an issue (a copy that could not be read, found first, kept
	// the sound one out of the library)
	keepInstead := func(found ExtendedFileInfo, file ExtendedFileInfo) bool {
		// a copy chosen in Issues wins
		if chosen[fullPath(file)] != chosen[fullPath(found)] {
			return chosen[fullPath(file)]
		}
		if flagged(file) != flagged(found) {
			return flagged(found)
		}
		// a copy the check vouched for whole wins over one it checked only partly, which keeps
		// the cleanup from deleting the other copies, unless the settings ask for a kind of file
		if keepDuplicate != settings.KEEP_NSP && keepDuplicate != settings.KEEP_XCI && partly(file) != partly(found) {
			return partly(found)
		}
		return PreferDuplicate(keepDuplicate, found, titlesInFile[found], file, titlesInFile[file])
	}
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

	// the files are read and added in chunks: a big first scan shows its games while it
	// runs, and what was read is cached even if the app stops before the end
	results := make([]metadataResult, len(files))
	total := len(candidates)
	var read atomic.Int64
	covers := []coverDownload{}
	partial, publishes := progress.(PartialLibraryReceiver)
	lastPublish := time.Now()
	for start := 0; start < len(candidates); start += scanChunkSize {
		chunk := candidates[start:min(start+scanChunkSize, len(candidates))]

		// read the files in parallel, with a bounded number of workers so a big library
		// does not saturate the disk, the network share or the CPU
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
		for _, i := range chunk {
			jobs <- i
		}
		close(jobs)
		workers.Wait()

		// newly read metadata is cached in one transaction, not one per file
		newEntries := map[string]interface{}{}
		for _, i := range chunk {
			if results[i].cacheKey != "" {
				newEntries[results[i].cacheKey] = results[i].metadata
			}
		}
		if len(newEntries) > 0 {
			if err := ldb.db.AddEntries(DB_TABLE_FILE_SCAN_METADATA, newEntries); err != nil {
				zap.S().Warnf("Failed to cache the metadata of %v files: %v", len(newEntries), err)
			}
		}

		// combine the results in the order of the files, so duplicates and old versions are
		// decided like in a sequential scan
		for _, i := range chunk {
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

			titlesInFile[file] = len(contentMap)
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

				kind, _ := metadataKind(metadata)

				//process Updates
				if kind == KIND_UPDATE {
					metadata.Type = "Update"

					if update, ok := switchTitle.Updates[metadata.Version]; ok && keepInstead(update.ExtendedInfo, file) {
						skipped[update.ExtendedInfo] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate update file (" + fullPath(file) + ")"}
						switchTitle.Updates[metadata.Version] = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
						continue
					} else if ok {
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
				if kind == KIND_BASE {
					metadata.Type = "Base"
					if switchTitle.BaseExist && keepInstead(switchTitle.File.ExtendedInfo, file) {
						skipped[switchTitle.File.ExtendedInfo] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate base file (" + fullPath(file) + ")"}
						switchTitle.File = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
						continue
					}
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
					} else if metadata.Version == dlc.Metadata.Version && keepInstead(dlc.ExtendedInfo, file) {
						skipped[dlc.ExtendedInfo] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + fullPath(file) + ")"}
						metadata.Type = "DLC"
						switchTitle.Dlc[metadata.TitleId] = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
						continue
					} else if metadata.Version == dlc.Metadata.Version {
						skipped[file] = SkippedFile{ReasonCode: REASON_DUPLICATE, ReasonText: "duplicate DLC file (" + fullPath(dlc.ExtendedInfo) + ")"}
						zap.S().Warnf("-->Duplicate DLC file found [%v] and [%v]", file.FileName, dlc.ExtendedInfo.FileName)
						continue
					}
				}
				//not an update, and not main TitleAttributes, so treat it as a DLC
				if older, ok := switchTitle.Dlc[metadata.TitleId]; ok && older.ExtendedInfo != file {
					// the older version stays listed, like an old update, instead of vanishing
					skipped[older.ExtendedInfo] = SkippedFile{ReasonCode: REASON_OLD_UPDATE, ReasonText: "old DLC file, newer version exist locally (" + fullPath(file) + ")"}
				}
				metadata.Type = "DLC"
				switchTitle.Dlc[metadata.TitleId] = SwitchFileInfo{ExtendedInfo: file, Metadata: metadata}
			}
		}

		// slow reads (a first scan) show what was found so far; fast ones (everything
		// cached) finish before anything is published
		if publishes && start+scanChunkSize < len(candidates) && time.Since(lastPublish) >= partialLibraryInterval {
			covers = assignCachedCovers(dataFolder, covers)
			partial.PartialLibrary(copyTitles(titles), copySkipped(skipped), start+len(chunk))
			lastPublish = time.Now()
		}
	}

	// covers not cached yet are downloaded after the scan, so a big library is shown
	// without waiting for thousands of downloads
	assignCachedCovers(dataFolder, covers)
	resolveKeptChains(skipped)
}

// PreferDuplicate tells whether the copy file is kept instead of found, the copy of the same
// title found first, by the choice of the settings (keep). A file with several titles holds
// other content too: it is never deleted while it holds anything the library keeps, so
// letting the app choose keeps it and makes the separate copy the duplicate.
func PreferDuplicate(keep string, found ExtendedFileInfo, foundTitles int, file ExtendedFileInfo, fileTitles int) bool {
	cartridge := func(f ExtendedFileInfo) bool {
		name := strings.ToLower(f.FileName)
		return strings.HasSuffix(name, ".xci") || strings.HasSuffix(name, ".xcz")
	}
	switch keep {
	case settings.KEEP_NSP:
		return cartridge(found) && !cartridge(file)
	case settings.KEEP_XCI:
		return !cartridge(found) && cartridge(file)
	case settings.KEEP_AUTO:
		if (foundTitles > 1) != (fileTitles > 1) {
			return fileTitles > 1
		}
		return file.Size > 0 && file.Size < found.Size
	}
	return false
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

// needsAttributes tells cached data of a game the titles database does not know, read before
// the app read the attributes of games or every language of their names apart: only the game
// itself can tell its name and that it is a demo, so it is read again, once.
func (ldb *LocalSwitchDBManager) needsAttributes(metadata map[string]*switchfs.ContentMetaAttributes) bool {
	if ldb.titles == nil {
		return false
	}
	for _, meta := range metadata {
		if meta == nil || meta.Ncap == nil || (meta.Ncap.AttributesRead && meta.Ncap.NamesRead && meta.Ncap.PublishersRead) {
			continue
		}
		// read before the names were read apart and left without any name: the Simplified
		// Chinese one was lost, whatever the titles database knows of the game
		if !meta.Ncap.NamesRead && !hasName(meta.Ncap) {
			return true
		}
		if prefix, err := TitleIDPrefix(meta.TitleId); err == nil {
			if _, known := ldb.titles.TitlesMap[prefix]; !known {
				return true
			}
		}
	}
	return false
}

// hasName tells whether a game gives itself a name in any language.
func hasName(nacp *switchfs.Nacp) bool {
	for _, title := range nacp.TitleName {
		if strings.Trim(title.Title, " -_.?·") != "" {
			return true
		}
	}
	return false
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
		if metadata != nil && !ldb.needsAttributes(metadata) {
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

// metadataKind is the kind of a content read from its metadata, which names it exactly,
// else from its title ID.
func metadataKind(metadata *switchfs.ContentMetaAttributes) (string, error) {
	switch strings.ToUpper(metadata.Type) {
	case "BASE":
		return KIND_BASE, nil
	case "UPD", "UPDATE":
		return KIND_UPDATE, nil
	case "DLC":
		return KIND_DLC, nil
	}
	return ContentKind(metadata.TitleId)
}
