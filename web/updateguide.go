package web

import (
	"bufio"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/settings"
)

// The update guide shows how to update the app where it runs. A container made with
// "docker run" (on Unraid also when the app was not installed from the Apps tab) cannot be
// updated by the Docker tab, so the guide writes the commands with the folders of this
// container, found in the mount table of the system.

const (
	containerDataFolder = "/usr/local/share/switch-library-manager-web"
	containerRomsFolder = "/mnt/roms"
)

// HostMount is a folder of the container and the folder of the server it comes from.
type HostMount struct {
	Container string
	Host      string
	// the host folder could not be found for sure and should be checked
	Uncertain bool
}

type UpdatePageData struct {
	GlobalPageData
	InContainer bool
	Mounts      []HostMount
	PUID        string
	PGID        string
	TZ          string
	Port        int
	Image       string
	Command     string
	Releases    string
}

// mountEntry is a line of /proc/self/mountinfo.
type mountEntry struct {
	root       string
	mountPoint string
	fsType     string
	source     string
}

// readMountInfo reads the mount table of the container.
func readMountInfo(reader io.Reader) []mountEntry {
	entries := []mountEntry{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		// id parent major:minor root mount-point options [optional...] - fstype source super-options
		fields := strings.Fields(scanner.Text())
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if len(fields) < 5 || separator < 0 || separator+2 >= len(fields) {
			continue
		}
		entries = append(entries, mountEntry{
			root:       unescapeMount(fields[3]),
			mountPoint: unescapeMount(fields[4]),
			fsType:     fields[separator+1],
			source:     fields[separator+2],
		})
	}
	return entries
}

// unescapeMount decodes the octal escapes of mountinfo, e.g. \040 for a space.
func unescapeMount(value string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(value)
}

// hostFolder finds the folder of the server mounted at a folder of the container.
func hostFolder(entries []mountEntry, folder string) (string, bool) {
	var best *mountEntry
	for i := range entries {
		entry := &entries[i]
		if folder == entry.mountPoint || strings.HasPrefix(folder, strings.TrimSuffix(entry.mountPoint, "/")+"/") {
			if best == nil || len(entry.mountPoint) > len(best.mountPoint) {
				best = entry
			}
		}
	}
	if best == nil || best.mountPoint == "/" {
		return "", false
	}
	rest := strings.TrimPrefix(folder, best.mountPoint)
	switch {
	// Unraid user shares are one file system mounted at /mnt/user
	case best.fsType == "fuse.shfs":
		return "/mnt/user" + best.root + rest, true
	// a disk or the cache pool: the root is relative to the disk, which is mounted somewhere
	// on the server; on Unraid usually /mnt/cache or /mnt/diskN
	default:
		return best.root + rest, false
	}
}

func (web *Web) updatePageData() UpdatePageData {
	appSettings := settings.ReadSettings(web.dataFolder)
	data := UpdatePageData{
		GlobalPageData: web.globalPageData("update"),
		PUID:           envOr("PUID", "99"),
		PGID:           envOr("PGID", "100"),
		TZ:             envOr("TZ", "Europe/Madrid"),
		Port:           appSettings.Port,
		Image:          "delfus77/switch-library-manager-web:latest",
		Releases:       "https://github.com/" + settings.REPOSITORY_OWNER + "/switch-library-manager-web/releases/latest",
	}
	if data.Port == 0 {
		data.Port = 3000
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return data
	}
	data.InContainer = true

	entries := []mountEntry{}
	if file, err := os.Open("/proc/self/mountinfo"); err == nil {
		entries = readMountInfo(file)
		file.Close()
	}
	folders := []string{containerDataFolder}
	for _, folder := range scanFolders(appSettings) {
		folder = filepath.ToSlash(folder)
		if !strings.HasPrefix(folder, containerDataFolder) {
			folders = append(folders, folder)
		}
	}
	if len(folders) == 1 {
		folders = append(folders, containerRomsFolder)
	}
	command := []string{"docker run -d --name switch-library-manager-web --restart unless-stopped",
		"-p " + strconv.Itoa(data.Port) + ":" + strconv.Itoa(data.Port),
		"-e PUID=" + data.PUID + " -e PGID=" + data.PGID + " -e TZ=" + data.TZ}
	seen := map[string]bool{}
	for _, folder := range folders {
		host, sure := hostFolder(entries, folder)
		mount := HostMount{Container: folder, Host: host, Uncertain: !sure}
		if host == "" {
			mount.Host = "/mnt/user/…"
		}
		if !seen[folder] {
			seen[folder] = true
			data.Mounts = append(data.Mounts, mount)
			command = append(command, "-v '"+mount.Host+"':'"+folder+"'")
		}
	}
	command = append(command, data.Image)
	data.Command = strings.Join(command, " ")
	return data
}

func envOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (web *Web) HandleUpdateGuide() {
	templates := web.mustParseTemplates(web.embedFS, "resources/layout.html", "resources/pages/update.html")
	web.router.HandleFunc("/update.html", func(w http.ResponseWriter, r *http.Request) {
		web.render(w, r, templates, web.updatePageData())
	}).Methods("GET")
}
