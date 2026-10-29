package switchfs

import "testing"

func TestPackageContents(t *testing.T) {
	for _, c := range []struct {
		games, updates, dlcs int
		want                 string
	}{{1, 1, 2, "1G+1U+2D"}, {0, 1, 0, "1U"}, {1, 0, 3, "1G+3D"}, {0, 0, 0, ""}} {
		if got := PackageContents(c.games, c.updates, c.dlcs); got != c.want {
			t.Errorf("%d %d %d: %q", c.games, c.updates, c.dlcs, got)
		}
	}
}

// Decimal units, rounded half up; nothing for a size not known.
func TestSizes(t *testing.T) {
	for size, want := range map[int64][2]string{
		2_650_000_000: {"2.7GB", "2650MB"},
		2_649_999_999: {"2.6GB", "2650MB"},
		242_064:       {"0.0GB", "0MB"},
		500_000:       {"0.0GB", "1MB"},
		0:             {"", ""},
		-1:            {"", ""},
	} {
		if gb, mb := SizeGB(size), SizeMB(size); gb != want[0] || mb != want[1] {
			t.Errorf("%d: %s %s", size, gb, mb)
		}
	}
}

func TestGameTitleID(t *testing.T) {
	for _, c := range []struct {
		id, kind string
		want     uint64
	}{
		{"0100BFC002B4E000", "BASE", 0x0100BFC002B4E000},
		{"0100bfc002b4e800", "UPD", 0x0100BFC002B4E000},
		{"0100B6400CA57088", "DLC", 0x0100B6400CA56000},
		{"0100B6400CA57001", "DLC", 0x0100B6400CA56000},
	} {
		if got, err := GameTitleID(c.id, c.kind); got != c.want || err != nil {
			t.Errorf("%s %s: %016X %v", c.id, c.kind, got, err)
		}
	}
	if _, err := GameTitleID("not an ID", "BASE"); err == nil {
		t.Error("an invalid title ID")
	}
}

// The settings tried take half the processors and all of them, on one, two and four files,
// lightest first, each once; the one chosen is the fastest, unless a lighter one is almost as
// fast.
func TestMeasureTrials(t *testing.T) {
	got := MeasureTrials(2, "fast", 4)
	if len(got) != 3 || got[0] != (MeasureTrial{Files: 1, Threads: 1}) || got[1] != (MeasureTrial{Files: 1, Threads: 2}) || got[2] != (MeasureTrial{Files: 2, Threads: 1}) {
		t.Fatalf("two processors: %+v", got)
	}
	got = MeasureTrials(16, "fast", 4)
	want := []MeasureTrial{{Files: 1, Threads: 8}, {Files: 2, Threads: 4}, {Files: 4, Threads: 2}, {Files: 1, Threads: 16}, {Files: 2, Threads: 8}, {Files: 4, Threads: 4}}
	if len(got) != len(want) {
		t.Fatalf("sixteen processors: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sixteen processors: %+v", got)
		}
	}
	if got := MeasureTrials(1, "fast", 4); len(got) != 1 || got[0] != (MeasureTrial{Files: 1, Threads: 1}) {
		t.Fatalf("one processor: %+v", got)
	}

	trials := []MeasureTrial{{1, 1, 100}, {1, 2, 180}, {2, 1, 186}, {1, 4, 260}, {2, 2, 270}, {3, 1, 200}}
	if best := ChooseTrial(trials); best != trials[3] {
		t.Fatalf("2x2 is less than 5%% faster than 1x4: %+v", best)
	}
	trials[4].Speed = 274
	if best := ChooseTrial(trials); best != trials[4] {
		t.Fatalf("2x2 is 5%% faster: %+v", best)
	}
	if best := ChooseTrial([]MeasureTrial{{Files: 2, Threads: 4}}); best != (MeasureTrial{Files: 1, Threads: 1}) {
		t.Fatalf("nothing measured: %+v", best)
	}
	if n := UsableProcessors(); n < 1 {
		t.Fatalf("%d processors", n)
	}
}
