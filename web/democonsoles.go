package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dtrunk90/switch-library-manager-web/internal/tasklog"
)

// The consoles of the demo: two made-up consoles of a made-up user, with what NXDock told of them
// (the system, what is installed and how much each game was played), a send going on to one of
// them and one that ended, so the page Consoles, Send to console and the network sends in Tasks
// can be seen. Nothing is sent: the demo refuses every change (demoReadOnly) and the paths of the
// installers answer nothing (netInstallGate).

// DEMO_PLAYER is the user of the demo, who paired the consoles.
const DEMO_PLAYER = "Player"

type demoConsole struct {
	key    KeyRow
	system netConsoleFacts
	// what is installed: the index of the game in demoGames, the update installed (0: none), the
	// DLC installed, where it is, and how long it was played
	games []demoConsoleGame
}

type demoConsoleGame struct {
	game, update, dlc int
	storage           string
	minutes, launches int64
	daysAgo           int
}

var demoConsoles = []demoConsole{
	{
		key: KeyRow{User: DEMO_PLAYER, ID: "d3e0c0a5011e0001", Name: "Living room Switch", Role: ROLE_CONSOLE, Hint: "0001"},
		system: netConsoleFacts{Firmware: "19.0.1", Atmosphere: "1.8.0", Emummc: true, SdFree: 61 << 30, SdTotal: 238 << 30,
			NandFree: 14 << 30, NandTotal: 25 << 30, Sigpatches: "sys-patch 1.5.4"},
		games: []demoConsoleGame{
			// one out of date: the library has a newer update and DLC the console lacks
			{game: 0, update: 2, dlc: 1, storage: "sd", minutes: 2840, launches: 61, daysAgo: 0},
			{game: 1, update: 4, dlc: 1, storage: "sd", minutes: 1210, launches: 44, daysAgo: 2},
			{game: 5, update: 5, dlc: 2, storage: "nand", minutes: 3920, launches: 87, daysAgo: 1},
			{game: 6, update: 3, dlc: 1, storage: "sd", minutes: 655, launches: 19, daysAgo: 9},
			{game: 12, update: 6, dlc: 3, storage: "sd", minutes: 1735, launches: 52, daysAgo: 4},
			{game: 17, update: 2, dlc: 1, storage: "sd", minutes: 486, launches: 23, daysAgo: 15},
		},
	},
	{
		key: KeyRow{User: DEMO_PLAYER, ID: "d3e0c0a5011e0002", Name: "Travel Switch Lite", Role: ROLE_CONSOLE, Hint: "0002"},
		system: netConsoleFacts{Firmware: "18.1.0", Atmosphere: "1.7.1", SdFree: 22 << 30, SdTotal: 119 << 30,
			NandFree: 9 << 30, NandTotal: 25 << 30, Sigpatches: "sys-patch 1.5.2"},
		games: []demoConsoleGame{
			{game: 4, update: 3, dlc: 2, storage: "sd", minutes: 930, launches: 31, daysAgo: 6},
			{game: 8, update: 3, dlc: 2, storage: "sd", minutes: 410, launches: 17, daysAgo: 12},
			{game: 16, update: 5, dlc: 1, storage: "sd", minutes: 275, launches: 12, daysAgo: 21},
		},
	},
}

// demoConsoleKeys are the consoles of the user of the demo, as their keys.
func demoConsoleKeys(user string) []KeyRow {
	keys := []KeyRow{}
	if !strings.EqualFold(user, DEMO_PLAYER) {
		return keys
	}
	for _, console := range demoConsoles {
		key := console.key
		key.Created = time.Now().AddDate(0, -3, 0)
		key.LastUsed = time.Now().Add(-12 * time.Minute)
		keys = append(keys, key)
	}
	return keys
}

// demoDlcId is the ID of a DLC of a game of the demo, as loadDemo makes them.
func demoDlcId(game int, dlc int) string {
	id := demoTitleId(game)
	return id[:12] + "1" + fmt.Sprintf("%03X", 1+dlc)
}

// writeDemoConsoles writes what the consoles of the demo told: what is installed on them and
// how much each game was played.
func (web *Web) writeDemoConsoles(now time.Time) {
	folder := filepath.Join(web.dataFolder, netInventoryFolder)
	if err := os.MkdirAll(folder, 0755); err != nil {
		return
	}
	for c, console := range demoConsoles {
		system := console.system
		inventory := netInventory{KeyId: console.key.ID, Console: console.key.Name, Owner: DEMO_PLAYER,
			Updated: now.Add(-time.Duration(12+c*170) * time.Minute), Items: []netInventoryItem{}, System: &system}
		for _, installed := range console.games {
			id := demoTitleId(installed.game)
			inventory.Items = append(inventory.Items, netInventoryItem{TitleId: id, Type: "game", Storage: installed.storage})
			if installed.update > 0 {
				inventory.Items = append(inventory.Items, netInventoryItem{TitleId: id[:13] + "800", Type: "update",
					Version: installed.update << 16, Storage: installed.storage})
			}
			for d := 0; d < installed.dlc; d++ {
				inventory.Items = append(inventory.Items, netInventoryItem{TitleId: demoDlcId(installed.game, d), Type: "dlc", Storage: installed.storage})
			}
			// and its language packs, as loadDemo numbers them
			for p := range demoLanguagePacks[demoGames[installed.game].name] {
				inventory.Items = append(inventory.Items, netInventoryItem{TitleId: demoDlcId(installed.game, 0xFF+p), Type: "dlc", Storage: installed.storage})
			}
			last := now.AddDate(0, 0, -installed.daysAgo).Add(-time.Duration(2+installed.game%5) * time.Hour)
			inventory.Play = append(inventory.Play, netPlayEntry{TitleId: id, Minutes: installed.minutes, Launches: installed.launches, LastPlayed: last.Unix()})
		}
		if data, err := json.Marshal(inventory); err == nil {
			os.WriteFile(web.netInventoryPath(console.key.ID), data, 0600)
		}
	}
}

// demoCover is the cover of a game of the demo by its name, as the sends show it.
func demoCover(name string) string {
	for i, game := range demoGames {
		if strings.EqualFold(game.name, name) || strings.HasPrefix(name, game.name+" – ") {
			return thumbUrl("/i/demo-" + demoTitleId(i) + ".png")
		}
	}
	return ""
}

// demoSendCycle is how long the made-up send to the first console takes, before it starts again.
const demoSendCycle = 10 * time.Minute

// demoNetSend tells the made-up send to the first console of the demo as NXDock would tell its
// queue: a game installed already, its update going and a DLC waiting, at a speed that changes a
// little, so Tasks and Consoles show it going on. It is told again on each look, from the time.
func (web *Web) demoNetSend(now time.Time) {
	console := demoConsoles[0].key
	game := demoGames[5]
	update, dlc := int64(1840<<20), int64(512<<20)
	elapsed := now.Sub(now.Truncate(demoSendCycle)).Seconds()
	speed := 36.5 + 4*float64(int64(elapsed)%7)/7
	percent := 20 + 60*elapsed/demoSendCycle.Seconds()
	left := int((1-percent/100)*float64(update)/1048576/speed) + 1
	jobs := []netQueueJob{
		{Id: 1, Game: game.name, State: "done", Size: 5 << 30, Percent: 100},
		{Id: 2, Game: game.name, Label: "Update 1.7.0", State: "running", Stage: "network", Size: update, Percent: percent, Speed: speed, Left: left},
		{Id: 3, Game: game.name, Label: "Bonus Chapter", State: "waiting", Size: dlc},
	}
	web.netSends.mutex.Lock()
	if run := web.netSends.live[console.ID]; run != nil && run.started.Before(now.Truncate(demoSendCycle).Add(-2*time.Minute)) {
		// a new turn of the cycle: the send starts again
		delete(web.netSends.live, console.ID)
	}
	web.netSends.mutex.Unlock()
	web.netSends.queue(console.ID, console.Name, DEMO_PLAYER, netQueueLive{Jobs: jobs}, demoCover, now)
	// as if the update had been going from the start of the cycle, so its average is near its speed
	web.netSends.mutex.Lock()
	if run := web.netSends.live[console.ID]; run != nil {
		run.started = now.Truncate(demoSendCycle).Add(-2 * time.Minute)
		if file := run.file("", 2); file != nil {
			file.started = now.Add(-time.Duration(percent / 100 * float64(update) / 1048576 / 38 * float64(time.Second)))
		}
		if file := run.file("", 1); file != nil {
			file.started, file.ended = run.started, now.Truncate(demoSendCycle)
		}
	}
	web.netSends.mutex.Unlock()
}

// demoNetSendTicker tells the made-up send every few seconds, so the pages that show it see it go.
func (web *Web) demoNetSendTicker() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		web.demoNetSend(time.Now())
		web.netSendChanged()
	}
}

// writeDemoSendTask keeps in the history of the tasks a send that ended to the second console of
// the demo, once.
func (web *Web) writeDemoSendTask(now time.Time) {
	console := demoConsoles[1].key
	for _, task := range web.taskLog().Snapshot() {
		if task.Kind == tasklog.TASK_NET_INSTALL && task.Console == console.Name {
			return
		}
	}
	started := now.Add(-3*time.Hour - 20*time.Minute)
	run := &netSendRun{id: console.ID, console: console.Name, user: DEMO_PLAYER, via: NET_VIA_NXDOCK, started: started}
	at := started
	for i, file := range []struct {
		game, label string
		size        int64
		seconds     int
	}{
		{demoGames[4].name, "", 3 << 30, 88},
		{demoGames[4].name, "Update 1.5.0", 410 << 20, 13},
		{demoGames[16].name, "", 2<<30 + 300<<20, 67},
	} {
		end := at.Add(time.Duration(file.seconds) * time.Second)
		run.files = append(run.files, &netSendFile{job: i + 1, name: file.game, label: file.label, cover: demoCover(file.game), size: file.size,
			done: file.size, state: tasklog.SENT_DONE, started: at, last: end, ended: end})
		at = end.Add(4 * time.Second)
	}
	run.last, run.ended = at, at
	if task, ok := netSendTask(run); ok {
		web.taskLog().Record(task)
	}
}
