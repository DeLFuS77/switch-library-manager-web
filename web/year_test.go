package web

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestYearReviewCountsOnlyWhatCameAfterTheFirstScan(t *testing.T) {
	web := demoWeb(t)
	now := time.Now()

	review := web.buildYearReview(now.Year(), "en", now)
	_, localDB := web.state.get()
	// four games of the demo were there at the first scan, last year
	if want := len(localDB.TitlesMap) - 4; review.GamesAdded != want {
		t.Fatalf("games added this year: %d, want %d", review.GamesAdded, want)
	}
	if review.Empty() || review.SizeAdded <= 0 || review.Biggest == nil || len(review.NewGames) == 0 {
		t.Fatalf("a full year: %+v", review)
	}
	months := 0
	for _, month := range review.Months {
		months += month.Games
	}
	if len(review.Months) != 12 || months != review.GamesAdded || review.BusiestMonth == "" {
		t.Fatalf("months %v, busiest %q", review.Months, review.BusiestMonth)
	}
	if len(review.Years) != 2 || review.Years[0] != now.Year() || review.FirstYear {
		t.Fatalf("years %v, first year %v", review.Years, review.FirstYear)
	}

	// last year only had the first scan: nothing was added
	if last := web.buildYearReview(now.Year()-1, "en", now); !last.Empty() || !last.FirstYear {
		t.Fatalf("the year of the first scan: %+v", last)
	}
}

func TestAddedIn(t *testing.T) {
	since := time.Date(2025, 3, 14, 18, 0, 0, 0, time.UTC)
	if addedIn(since.Add(20*time.Second), since, 2025) {
		t.Fatal("found by the first scan")
	}
	if !addedIn(since.Add(48*time.Hour), since, 2025) || addedIn(since.Add(48*time.Hour), since, 2026) {
		t.Fatal("added two days later, in 2025")
	}
	if addedIn(time.Time{}, since, 2025) {
		t.Fatal("never found")
	}
}

func TestYearPage(t *testing.T) {
	web := demoWeb(t)
	year := strconv.Itoa(time.Now().Year())
	for _, query := range []string{"", "?y=" + year, "?y=1999", "?y=abc"} {
		recorder := httptest.NewRecorder()
		web.router.ServeHTTP(recorder, httptest.NewRequest("GET", "/year.html"+query, nil))
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "Your "+year+" on Switch") {
			t.Fatalf("%q: %d", query, recorder.Code)
		}
	}
}

func TestYearReviewOldestIsOlderThanNewest(t *testing.T) {
	web := demoWeb(t)
	review := web.buildYearReview(time.Now().Year(), "en", time.Now())
	if review.Oldest == nil || review.Newest == nil || !review.Oldest.ReleaseDate.Before(review.Newest.ReleaseDate) {
		t.Fatalf("oldest %+v, newest %+v", review.Oldest, review.Newest)
	}
	for _, game := range review.NewGames {
		if game.Size > review.Biggest.Size {
			t.Fatalf("%s is bigger than the biggest, %s", game.Name, review.Biggest.Name)
		}
	}
}
