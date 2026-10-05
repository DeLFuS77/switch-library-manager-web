package web

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dtrunk90/switch-library-manager-web/db"
	"github.com/dtrunk90/switch-library-manager-web/switchfs"
)

// Demo mode (SLM_DEMO=true) shows a made-up library, for screenshots and to try the app
// without games: the titles, publishers and covers are invented, nothing is read from
// disk or downloaded, and changes are refused.

const DEMO_DISABLED = "Demo mode: changes are disabled."

func isDemoMode() bool {
	return strings.EqualFold(os.Getenv("SLM_DEMO"), "true")
}

type demoGame struct {
	name, publisher, description string
	released                     int
	// updates available, and how many of them are in the library (-1: game not owned)
	updates, ownedUpdate int
	dlc, ownedDlc        int
	hue                  float64
}

var demoGames = []demoGame{
	{"Starlight Odyssey", "Lumen Works", "Sail a paper ship between the stars and map a galaxy that redraws itself every night.", 20230317, 4, 4, 3, 3, 0.62},
	{"Pixel Kart Rally", "Tiny Tyre Games", "Drift through 48 hand-drawn tracks with up to eight friends.", 20220610, 6, 4, 2, 1, 0.02},
	{"Moss & Mortar", "Hollow Oak", "Build a village on the back of a sleeping giant, one stone at a time.", 20210902, 3, 3, 1, 0, 0.33},
	{"Neon Drift 2088", "Gridline", "A synthwave racer where the road is built from the music you play.", 20240125, 2, 2, 0, 0, 0.85},
	{"Tidecaller", "Blue Lantern", "Command the tides to solve puzzles across a drowned archipelago.", 20200514, 5, 5, 2, 2, 0.55},
	{"Gearheart Tactics", "Cogwheel Studio", "Turn-based battles with clockwork armies and a stubborn robot general.", 20221104, 7, 5, 4, 2, 0.08},
	{"Lantern Fields", "Firefly Collective", "A calm farming story about lighting up a valley that forgot the sun.", 20230822, 3, 3, 1, 1, 0.14},
	{"Echoes of Aurora", "Northwind", "Explore frozen ruins where every sound you make changes the world.", 20190926, 2, 2, 0, 0, 0.48},
	{"Bubble Bistro", "Soda Pop Games", "Run a café under the sea for very particular fish.", 20210318, 4, 3, 2, 2, 0.92},
	{"Iron Petal", "Verdant Forge", "A fast action game about a knight made of flowers and steel.", 20240411, 1, 0, 1, 0, 0.97},
	{"Cloudline Express", "Skyrail", "Drive a train above the clouds and keep every passenger on time.", 20220127, 3, 3, 0, 0, 0.58},
	{"Quiet Hollow", "Hollow Oak", "Solve a mystery in a village where nobody is allowed to speak.", 20201029, 2, 2, 1, 1, 0.75},
	{"Robo Rumble League", "Gridline", "Build a robot, then throw it at your friends in a stadium.", 20230609, 8, 6, 5, 3, 0.03},
	{"Saltwind", "Blue Lantern", "A sailing adventure across a world of endless desert seas.", 20211208, 4, 4, 2, 2, 0.12},
	{"Garden of Glass", "Lumen Works", "Grow crystal plants that bend light to open new paths.", 20220915, 2, 2, 0, 0, 0.45},
	{"Fable Forge", "Cogwheel Studio", "Write your own fairy tales and watch the characters act them out.", 20240229, 1, 1, 2, 0, 0.68},
	{"Comet Couriers", "Tiny Tyre Games", "Deliver parcels across a solar system in a very small rocket.", 20190711, 5, 5, 1, 1, 0.52},
	{"Ember Trail", "Firefly Collective", "A roguelike hike where the campfire is your only save point.", 20230131, 3, 2, 1, 1, 0.05},
	// in the catalog, not in the library
	{"Velvet Thunder", "Northwind", "A rhythm brawler set in a city of jazz clubs.", 20240718, 1, -1, 1, 0, 0.88},
	{"Paper Lighthouse", "Soda Pop Games", "Fold the world like paper to guide ships home.", 20231012, 0, -1, 0, 0, 0.6},
	{"Hollow Crown", "Verdant Forge", "An action role-playing game in a kingdom that lost its king and its colors.", 20220421, 3, -1, 2, 0, 0.78},
	{"Sky Garden Story", "Firefly Collective", "Keep a floating garden alive with the help of grumpy bees.", 20240905, 0, -1, 0, 0, 0.27},
	{"Circuit Breakers", "Gridline", "A cooperative puzzle game about rewiring a very angry factory.", 20210610, 2, -1, 1, 0, 0.18},
}

func demoTitleId(index int) string {
	return fmt.Sprintf("0100D%07X", index+1) + "0000"
}

// loadDemo puts the made-up library in place and writes its covers.
func (web *Web) loadDemo() {
	imgFolder := filepath.Join(web.dataFolder, "img")
	os.MkdirAll(imgFolder, 0755)
	baseFolder := filepath.Join(string(filepath.Separator)+"games", "demo")
	file := func(name string, size int64) db.ExtendedFileInfo {
		return db.ExtendedFileInfo{FileName: name, BaseFolder: baseFolder, Size: size}
	}

	switchDB := &db.SwitchTitlesDB{TitlesMap: map[string]*db.SwitchTitle{}, Localized: map[string]map[string]db.LocalizedTitle{}}
	localDB := &db.LocalSwitchFilesDB{TitlesMap: map[string]*db.SwitchGameFiles{}, Skipped: map[db.ExtendedFileInfo]db.SkippedFile{}}

	for i, game := range demoGames {
		id := demoTitleId(i)
		icon := "demo-" + id + ".png"
		banner := "demo-" + id + "-banner.png"
		writeDemoCover(filepath.Join(imgFolder, icon), 512, 512, game.hue, i)
		writeDemoCover(filepath.Join(imgFolder, banner), 1280, 720, game.hue, i)

		title := &db.SwitchTitle{
			Attributes: db.TitleAttributes{Id: id, Name: game.name, Publisher: game.publisher, Description: game.description,
				ReleaseDate: game.released, Region: "US", IconUrl: "/i/" + icon, BannerUrl: "/i/" + banner,
				Size: (2 + i%9) << 30, Version: "0"},
			Updates: map[int]string{},
			Dlc:     map[string]db.TitleAttributes{},
		}
		year, month := game.released/10000, game.released/100%100
		for u := 1; u <= game.updates; u++ {
			month++
			if month > 12 {
				year, month = year+1, 1
			}
			title.Updates[u<<16] = fmt.Sprintf("%d-%02d-15", year, month)
		}
		dlcNames := []string{"Expansion Pass", "Soundtrack", "Costume Pack", "Bonus Chapter", "Deluxe Upgrade"}
		for d := 0; d < game.dlc; d++ {
			// DLC IDs: the next 13th digit of the game, then a number
			dlcId := id[:12] + "1" + fmt.Sprintf("%03X", 1+d)
			title.Dlc[dlcId] = db.TitleAttributes{Id: dlcId, Name: game.name + " – " + dlcNames[d%len(dlcNames)], IconUrl: "/i/" + banner}
		}
		switchDB.TitlesMap[strings.ToLower(id[:13])] = title

		if game.ownedUpdate < 0 {
			continue
		}
		size := int64(1+i%7) << 30
		files := &db.SwitchGameFiles{
			BaseExist: true,
			File:      db.SwitchFileInfo{ExtendedInfo: file(fmt.Sprintf("%s [%s][v0].nsz", game.name, id), size), Metadata: &switchfs.ContentMetaAttributes{TitleId: id, Type: "BaseGame"}},
			Updates:   map[int]db.SwitchFileInfo{},
			Dlc:       map[string]db.SwitchFileInfo{},
			Icon:      icon,
			Banner:    banner,
		}
		if game.ownedUpdate > 0 {
			version := game.ownedUpdate << 16
			updateId := id[:13] + "800"
			files.Updates[version] = db.SwitchFileInfo{
				ExtendedInfo: file(fmt.Sprintf("%s [%s][v%d].nsp", game.name, updateId, version), 256<<20),
				Metadata:     &switchfs.ContentMetaAttributes{TitleId: updateId, Version: version, Type: "Update"},
			}
			files.LatestUpdate = version
		}
		owned := 0
		for dlcId, dlc := range title.Dlc {
			if owned >= game.ownedDlc {
				break
			}
			owned++
			files.Dlc[dlcId] = db.SwitchFileInfo{
				ExtendedInfo: file(fmt.Sprintf("%s [%s][v0].nsp", dlc.Name, dlcId), 512<<20),
				Metadata:     &switchfs.ContentMetaAttributes{TitleId: dlcId, Type: "AddOnContent"},
			}
		}
		localDB.TitlesMap[strings.ToLower(id[:13])] = files
		localDB.NumFiles += 1 + len(files.Updates) + len(files.Dlc)
	}

	// a few issues, like in a real library
	localDB.Skipped[file("notes.txt", 2048)] = db.SkippedFile{ReasonCode: db.REASON_UNSUPPORTED_TYPE, ReasonText: "file type is not supported"}
	localDB.Skipped[file("Starlight Odyssey ["+demoTitleId(0)[:13]+"800][v65536].nsp", 200<<20)] = db.SkippedFile{ReasonCode: db.REASON_OLD_UPDATE,
		ReasonText: "old update file, newer update exist locally (" + filepath.Join(baseFolder, "Starlight Odyssey ["+demoTitleId(0)[:13]+"800][v262144].nsp") + ")"}
	localDB.Skipped[file("Pixel Kart Rally ["+demoTitleId(1)+"][v0] (1).nsz", 3<<30)] = db.SkippedFile{ReasonCode: db.REASON_DUPLICATE,
		ReasonText: "duplicate base file (" + filepath.Join(baseFolder, "Pixel Kart Rally ["+demoTitleId(1)+"][v0].nsz") + ")"}
	localDB.NumFiles += 3

	web.state.set(switchDB, localDB)
	web.sugarLogger.Infof("[Demo mode: %d made-up games]", len(localDB.TitlesMap))
}

// writeDemoCover draws an abstract cover: a gradient with soft circles and a horizon.
func writeDemoCover(path string, width, height int, hue float64, seed int) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	hash := fnv.New32a()
	fmt.Fprint(hash, seed)
	random := hash.Sum32()
	next := func() float64 {
		random ^= random << 13
		random ^= random >> 17
		random ^= random << 5
		return float64(random%10000) / 10000
	}
	type circle struct{ x, y, r, hue float64 }
	circles := make([]circle, 5)
	for i := range circles {
		circles[i] = circle{next() * float64(width), next() * float64(height) * 0.7, (0.08 + next()*0.22) * float64(width), hue + (next()-0.5)*0.25}
	}
	horizon := float64(height) * (0.6 + next()*0.15)

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		t := float64(y) / float64(height)
		for x := 0; x < width; x++ {
			r, g, b := hsl(hue+0.08*t, 0.65, 0.18+0.35*t)
			for _, c := range circles {
				d := math.Hypot(float64(x)-c.x, float64(y)-c.y)
				if d < c.r {
					cr, cg, cb := hsl(c.hue, 0.75, 0.62)
					alpha := 0.55 * (1 - d/c.r) * (1 - d/c.r)
					r, g, b = r+(cr-r)*alpha, g+(cg-g)*alpha, b+(cb-b)*alpha
				}
			}
			if wave := horizon + 18*math.Sin(float64(x)/float64(width)*math.Pi*3+hue*10); float64(y) > wave {
				hr, hg, hb := hsl(hue+0.5, 0.45, 0.12)
				r, g, b = r*0.3+hr*0.7, g*0.3+hg*0.7, b*0.3+hb*0.7
			}
			img.SetRGBA(x, y, color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255})
		}
	}
	out, err := os.Create(path)
	if err != nil {
		return
	}
	defer out.Close()
	png.Encode(out, img)
}

func hsl(h, s, l float64) (float64, float64, float64) {
	h = h - math.Floor(h)
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h*6, 2)-1))
	m := l - c/2
	var r, g, b float64
	switch int(h * 6) {
	case 0:
		r, g, b = c, x, 0
	case 1:
		r, g, b = x, c, 0
	case 2:
		r, g, b = 0, c, x
	case 3:
		r, g, b = 0, x, c
	case 4:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	return r + m, g + m, b + m
}

// demoReadOnly refuses every change in demo mode.
func (web *Web) demoReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.URL.Path != "/login.html" && r.URL.Path != "/logout" {
				writeGlobalError(w, http.StatusForbidden, web.requestLanguage(r), DEMO_DISABLED)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
