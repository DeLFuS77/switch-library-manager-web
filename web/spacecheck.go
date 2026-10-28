package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"

	"github.com/dtrunk90/switch-library-manager-web/settings"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// SpaceShort is a volume where the new files of a task may not fit: the folder they go to, the
// most they may take there at once, and what is free.
type SpaceShort struct {
	Folder string `json:"folder"`
	Need   int64  `json:"need"`
	Free   int64  `json:"free"`
}

// spaceWrite is a new file a task writes: the folder it goes to, the most it can take, and the
// folder of its original.
type spaceWrite struct {
	folder string
	need   int64
	source string
}

// startWriting starts or queues a task that writes files, once the free space is weighed: when
// the new files may not fit, the page is told where, and starts it anyway with space_ok.
func (web *Web) startWriting(w http.ResponseWriter, r *http.Request, task QueuedTask) {
	if r.FormValue("space_ok") != "true" {
		if short := web.spaceShort(task); len(short) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"space": short})
			return
		}
	}
	web.startOrQueue(w, task)
}

// spaceShort weighs the new files of a task: each as big as its original, an NSZ or XCZ
// decompressed as big as its content, a pack as the files it is made of. New files are written
// beside their originals.
func (web *Web) spaceShort(task QueuedTask) []SpaceShort {
	writes := []spaceWrite{}
	switch task.Kind {
	case TASK_COMPRESS, TASK_DECOMPRESS, TASK_CONVERT, TASK_SPLIT:
		for _, path := range task.Paths {
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			need := info.Size()
			if task.Kind == TASK_DECOMPRESS {
				if facts, err := switchfs.DescribeFile(path); err == nil && facts.DecompressedSize > need {
					need = facts.DecompressedSize
				}
			}
			writes = append(writes, spaceWrite{folder: filepath.Dir(path), need: need, source: filepath.Dir(path)})
		}
	case TASK_PACK:
		for _, path := range task.Paths {
			if _, sources, size, err := web.packTarget(path, ""); err == nil {
				writes = append(writes, spaceWrite{folder: filepath.Dir(sources[0]), need: size, source: filepath.Dir(sources[0])})
			}
		}
	default:
		return nil
	}
	atOnce := 1
	if task.Kind == TASK_COMPRESS || task.Kind == TASK_DECOMPRESS || task.Kind == TASK_CONVERT {
		atOnce = filesAtOnce(settings.ReadSettings(web.dataFolder).CompressFilesAtOnce)
	}
	return weighSpace(writes, task.Delete, atOnce, volumeOf, diskFree)
}

// weighSpace adds up the new files by volume. A file whose original is deleted on the same volume
// gives its room back once it is done, so of those only the ones written at once count; with
// the room the app keeps free beside them (64 MB).
func weighSpace(writes []spaceWrite, deleted bool, atOnce int, volume func(string) string, free func(string) (uint64, error)) []SpaceShort {
	type drive struct {
		folder string
		kept   int64
		freed  []int64
	}
	drives := map[string]*drive{}
	order := []string{}
	for _, write := range writes {
		key := volume(write.folder)
		if drives[key] == nil {
			drives[key] = &drive{folder: write.folder}
			order = append(order, key)
		}
		if deleted && volume(write.source) == key {
			drives[key].freed = append(drives[key].freed, write.need)
		} else {
			drives[key].kept += write.need
		}
	}
	short := []SpaceShort{}
	for _, key := range order {
		d := drives[key]
		sort.Slice(d.freed, func(i, j int) bool { return d.freed[i] > d.freed[j] })
		need := d.kept
		for i := 0; i < len(d.freed) && i < max(1, atOnce); i++ {
			need += d.freed[i]
		}
		left, err := free(d.folder)
		if err == nil && need+(64<<20) > int64(left) {
			short = append(short, SpaceShort{Folder: d.folder, Need: need, Free: int64(left)})
		}
	}
	return short
}
