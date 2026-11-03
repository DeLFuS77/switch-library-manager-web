// Package awake keeps the computer from going to sleep while a task works on the files: a
// compression or a check left for the night would else stop halfway. Each system does it its
// own way; where it cannot (a container, where the host decides), nothing happens.
package awake

import "sync"

// the holds of the tasks at work: the computer is kept awake while one is not paused
var state struct {
	mutex   sync.Mutex
	active  int
	release func()
}

// BlockSleep keeps the computer from going to sleep until the function it gives is called.
var BlockSleep = preventSleep

// Hold keeps the computer awake for a task, from Keep until Release.
type Hold struct {
	paused   bool
	released bool
}

// Keep keeps the computer awake until the hold it gives is released.
func Keep() *Hold {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	state.active++
	update()
	return &Hold{}
}

// Pause lets the computer sleep while the task is paused, and keeps it awake again once the
// task goes on. A nil hold does nothing.
func (h *Hold) Pause(paused bool) {
	if h == nil {
		return
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if h.released || h.paused == paused {
		return
	}
	h.paused = paused
	if paused {
		state.active--
	} else {
		state.active++
	}
	update()
}

// Release ends the hold; once more, or on a nil hold, it does nothing.
func (h *Hold) Release() {
	if h == nil {
		return
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if h.released {
		return
	}
	h.released = true
	if !h.paused {
		state.active--
	}
	update()
}

// update keeps the computer awake while a hold wants it so, and lets it sleep once none does;
// the mutex is held.
func update() {
	switch {
	case state.active > 0 && state.release == nil:
		state.release = BlockSleep()
	case state.active == 0 && state.release != nil:
		state.release()
		state.release = nil
	}
}
