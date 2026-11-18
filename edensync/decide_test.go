package edensync

import "testing"

// Every case of the decision: the last time in step, the folder now, and the newest saves of the
// library of a console and of an emulator.
func TestDecide(t *testing.T) {
	console := func(id string, when int64, hash string) *LibrarySave {
		return &LibrarySave{Id: id, When: when, Hash: hash}
	}
	emulator := func(id string, when int64, hash string) *LibrarySave {
		return &LibrarySave{Id: id, When: when, Hash: hash, Emulator: true}
	}
	synced := &Synced{SaveId: "c1", Hash: "h1", Seen: 100, SeenIds: []string{"c1"}}
	cases := []struct {
		name              string
		last              *Synced
		local             string
		console, emulator *LibrarySave
		want              Action
		save              string
		inStep            string
	}{
		{name: "nothing anywhere", want: DoNothing},
		{name: "only the emulator has a save", local: "h1", want: DoUpload},
		{name: "only the console has one", console: console("c1", 100, "h1"), want: DoBring, save: "c1"},
		{name: "the emulator has none yet, the newest goes in", console: console("c1", 100, ""), emulator: emulator("e1", 200, ""), want: DoBring, save: "e1"},
		{name: "same files, never synced", local: "h1", console: console("c1", 100, "h1"), want: DoNothing, inStep: "c1"},
		{name: "different files, never synced", local: "h2", console: console("c1", 100, "h1"), want: DoAsk, save: "c1"},
		{name: "in step", last: synced, local: "h1", console: console("c1", 100, "h1"), want: DoNothing, inStep: "c1"},
		{name: "in step, hash unknown", last: synced, local: "h1", console: console("c1", 100, ""), want: DoNothing},
		{name: "the emulator changed", last: synced, local: "h2", console: console("c1", 100, "h1"), want: DoUpload},
		{name: "the console changed", last: synced, local: "h1", console: console("c2", 150, "h3"), want: DoBring, save: "c2"},
		{name: "another computer changed", last: synced, local: "h1", console: console("c1", 100, "h1"), emulator: emulator("e2", 160, "h4"), want: DoBring, save: "e2"},
		{name: "both changed", last: synced, local: "h2", console: console("c2", 150, "h3"), want: DoAsk, save: "c2"},
		{name: "both changed to the same files", last: synced, local: "h3", console: console("c2", 150, "h3"), want: DoNothing, inStep: "c2"},
		{name: "a new save with the files synced", last: synced, local: "h1", console: console("c2", 150, "h1"), want: DoNothing, inStep: "c2"},
		{name: "an older save is not new", last: synced, local: "h1", console: console("c0", 90, "h0"), want: DoNothing},
		{name: "a save of the same second not seen", last: synced, local: "h1", console: console("c9", 100, "h9"), want: DoBring, save: "c9"},
		{name: "a save of the same second seen", last: &Synced{SaveId: "e1", Hash: "h1", Seen: 100, SeenIds: []string{"e1", "c9"}}, local: "h1", console: console("c9", 100, "h9"), want: DoNothing},
		{name: "deleted in the emulator, nothing new", last: synced, console: console("c1", 100, "h1"), want: DoNothing},
		{name: "deleted in the emulator, something new", last: synced, console: console("c2", 150, "h3"), want: DoBring, save: "c2"},
		{name: "deleted in the library", last: synced, local: "h1", want: DoUpload},
		{name: "a console and an emulator of the same time: the console", local: "", console: console("c1", 100, ""), emulator: emulator("e1", 100, ""), want: DoBring, save: "c1"},
	}
	for _, c := range cases {
		got := Decide(c.last, c.local, c.console, c.emulator)
		if got.Action != c.want {
			t.Errorf("%s: %v, want %v", c.name, got.Action, c.want)
			continue
		}
		if (c.save == "") != (got.Save == nil) || (got.Save != nil && got.Save.Id != c.save) {
			t.Errorf("%s: save %+v, want %q", c.name, got.Save, c.save)
		}
		if (c.inStep == "") != (got.InStep == nil) || (got.InStep != nil && got.InStep.Id != c.inStep) {
			t.Errorf("%s: in step with %+v, want %q", c.name, got.InStep, c.inStep)
		}
	}
	if DoAsk.String() != "conflict" || DoNothing.String() != "nothing" {
		t.Error("the names of the actions")
	}
}
