package pagination

import "testing"

func TestCalculate(t *testing.T) {
	tests := []struct {
		name                         string
		page, perPage, items         int
		wantPage, wantStart, wantEnd int
		wantPages                    int
	}{
		{"first page", 1, 10, 25, 1, 0, 10, 3},
		{"last partial page", 3, 10, 25, 3, 20, 25, 3},
		{"page beyond the end is clamped", 9, 10, 25, 3, 20, 25, 3},
		{"page zero", 0, 10, 25, 1, 0, 10, 3},
		{"negative page", -4, 10, 25, 1, 0, 10, 3},
		{"zero per page", 1, 0, 3, 1, 0, 1, 3},
		{"no items", 1, 10, 0, 1, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := Calculate(tt.page, tt.perPage, tt.items)
			if p.CurrentPage != tt.wantPage || p.Start != tt.wantStart || p.End != tt.wantEnd || p.NumPages != tt.wantPages {
				t.Fatalf("Calculate(%d, %d, %d) = %+v", tt.page, tt.perPage, tt.items, p)
			}
			if p.Start < 0 || p.End < p.Start || p.End > tt.items {
				t.Fatalf("invalid slice bounds: %+v", p)
			}
		})
	}
}

func TestCalculatePrevNext(t *testing.T) {
	p := Calculate(2, 10, 25)
	if !p.HasPrev || p.PrevPage != 1 || !p.HasNext || p.NextPage != 3 {
		t.Fatalf("unexpected navigation: %+v", p)
	}
	p = Calculate(3, 10, 25)
	if p.HasNext {
		t.Fatalf("last page has a next page: %+v", p)
	}
}
