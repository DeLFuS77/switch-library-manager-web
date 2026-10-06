package db

import (
	"reflect"
	"testing"
)

func TestGenresAndLanguagesAreNormalized(t *testing.T) {
	same := func(value string) string { return value }
	if got := normalizeGenres([]string{"アクション", "Action", "어드벤처", "動作", "Updates", " rpg "}, same); !reflect.DeepEqual(got, []string{"Action", "Adventure", "RPG"}) {
		t.Fatalf("genres of every store become one English name: %v", got)
	}
	if got := normalizeGenres([]string{"Updates"}, same); got != nil {
		t.Fatalf("unknown genres are left out: %v", got)
	}
	if got := normalizeLanguages([]string{"ja", "EN", "zh", "zh", ""}, same); !reflect.DeepEqual(got, []string{"en", "ja", "zh"}) {
		t.Fatalf("languages once, in lower case: %v", got)
	}
}
