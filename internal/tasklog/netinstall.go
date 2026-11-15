package tasklog

import (
	"fmt"
	"time"
)

// Installing over the network, as the Tasks page keeps it once it ended: the files a console
// was sent while it installed, one task per console and run (see web/netprogress.go). While it
// runs it is no task of the log: it is only shown, and the queue of the tasks on files does not
// wait for it.

const (
	// files sent to a console over the network
	TASK_NET_INSTALL = "netinstall"
	// what a console asked for
	TRIGGER_CONSOLE = "console"
)

// how a file sent to a console ended
const (
	SENT_DONE = "done"
	// the console stopped asking for it before it had it whole
	SENT_CUT       = "cut"
	SENT_FAILED    = "failed"
	SENT_CANCELLED = "cancelled"
)

// the notes of an install over the network that did not go well
const (
	NOTE_NET_FAILED    = "The console could not install what it was sent."
	NOTE_NET_CANCELLED = "The install was cancelled on the console."
)

func init() {
	NotWorthTelling[NOTE_NET_CANCELLED] = true
}

// SentFile is a file sent to a console, as it ended.
type SentFile struct {
	Name string `json:"name"`
	// game, update or dlc, served to DBI or Tinfoil; what NXDock does not tell
	Kind  string `json:"kind,omitempty"`
	Label string `json:"label,omitempty"`
	Cover string `json:"cover,omitempty"`
	Size  int64  `json:"size"`
	// how much of it the console got
	Done int64 `json:"done"`
	// how long it took, from the first request for it to the last
	Seconds float64 `json:"seconds"`
	State   string  `json:"state"`
	Error   string  `json:"error,omitempty"`
}

// Average is the speed it went at, in MB/s.
func (f SentFile) Average() float64 {
	if f.Seconds <= 0 {
		return 0
	}
	return float64(f.Done) / 1048576 / f.Seconds
}

// Took is how long it took, as "2m 28s".
func (f SentFile) Took() string {
	return ShortDuration(time.Duration(f.Seconds * float64(time.Second)))
}

// ShortDuration is a duration in hours and minutes, minutes and seconds, or seconds: "1h 5m",
// "2m 08s", "40s".
func ShortDuration(d time.Duration) string {
	d = d.Round(time.Second)
	hours, minutes, seconds := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %02ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}

// SentCount counts the files sent that ended so.
func (t Task) SentCount(state string) int {
	count := 0
	for _, file := range t.Sent {
		if file.State == state {
			count++
		}
	}
	return count
}

// SentBytes is how much the console got of all the files.
func (t Task) SentBytes() int64 {
	var bytes int64
	for _, file := range t.Sent {
		bytes += file.Done
	}
	return bytes
}

// SentAverage is the speed of the whole run in MB/s: what the console got over the time it took
// to send each file, without the time it asked for nothing.
func (t Task) SentAverage() float64 {
	seconds := 0.0
	for _, file := range t.Sent {
		seconds += file.Seconds
	}
	if seconds <= 0 {
		return 0
	}
	return float64(t.SentBytes()) / 1048576 / seconds
}

// SentFastest is the best average of the files, for the bars that compare them.
func (t Task) SentFastest() float64 {
	fastest := 0.0
	for _, file := range t.Sent {
		fastest = max(fastest, file.Average())
	}
	return fastest
}

// SpeedWidth is the width of the bar of a file, as a share of the fastest: "73".
func SpeedWidth(average float64, fastest float64) int {
	if fastest <= 0 {
		return 0
	}
	return int(min(100, max(2, average/fastest*100)))
}

// Record adds a task that already ended, as an install over the network ends: it is not told
// as ended or failed to anyone, as the console tells it itself.
func (l *Log) Record(task Task) int64 {
	l.mutex.Lock()
	defer l.mutex.Unlock()
	l.nextId++
	task.Id = l.nextId
	l.tasks = append([]*Task{&task}, l.tasks...)
	l.changed(true)
	return task.Id
}
