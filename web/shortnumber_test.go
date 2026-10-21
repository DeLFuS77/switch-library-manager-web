package web

import "testing"

// Big counts are written short on phones, each language its way.
func TestShortNumbers(t *testing.T) {
	for _, c := range []struct {
		lang  string
		value int
		want  string
	}{
		{"es", 24943, "25k"},
		{"en", 12044, "12k"},
		{"es", 1499999, "1500k"},
		{"ja", 24943, "2.5万"},
		{"zh", 120000, "12万"},
		{"ko", 12899, "1.3만"},
	} {
		if got := formatShortNumber(c.lang, c.value); got != c.want {
			t.Errorf("%s %d: %q, want %q", c.lang, c.value, got, c.want)
		}
	}
}
