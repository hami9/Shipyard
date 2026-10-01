package backup

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExpired(t *testing.T) {
	at := func(s string) time.Time {
		tm, err := time.Parse("2006-01-02 15:04", s)
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	// 2026-09-01 is a Tuesday: nightly backups on September 1 to 20.
	var nightly []time.Time
	for d := 1; d <= 20; d++ {
		nightly = append(nightly, time.Date(2026, 9, d, 2, 30, 0, 0, time.UTC))
	}
	for name, tc := range map[string]struct {
		times         []time.Time
		daily, weekly int
		want          string // expired, oldest first, as month-day hour
	}{
		"none": {nil, 14, 8, ""},
		"one":  {nightly[:1], 14, 8, ""},
		// Dailies keep the 7th to the 20th. The week of August 31 keeps its
		// newest, the 6th; the 1st to the 5th go.
		"dailies and weeklies": {nightly, 14, 8, "09-01 02,09-02 02,09-03 02,09-04 02,09-05 02"},
		// Weeklies: the 20th (also the daily), the 13th, and the 6th.
		"weeklies only matter": {nightly, 1, 3, "09-01 02,09-02 02,09-03 02,09-04 02,09-05 02,09-07 02,09-08 02,09-09 02,09-10 02,09-11 02,09-12 02,09-14 02,09-15 02,09-16 02,09-17 02,09-18 02,09-19 02"},
		"dailies only":         {nightly, 3, 0, "09-01 02,09-02 02,09-03 02,09-04 02,09-05 02,09-06 02,09-07 02,09-08 02,09-09 02,09-10 02,09-11 02,09-12 02,09-13 02,09-14 02,09-15 02,09-16 02,09-17 02"},
		// Two on one day: the newer one holds the day.
		"same day": {[]time.Time{at("2026-09-20 02:30"), at("2026-09-20 10:00"), at("2026-09-19 02:30")}, 14, 8, "09-20 02"},
		// The newest always stays, whatever the limits.
		"newest stays": {[]time.Time{at("2026-09-20 02:30"), at("2026-09-19 02:30")}, 0, 0, "09-19 02"},
		// Unsorted input, and a year boundary inside one ISO week (2026-W53).
		"year boundary": {[]time.Time{at("2027-01-01 02:30"), at("2026-12-28 02:30"), at("2026-12-31 02:30")}, 1, 1, "12-28 02,12-31 02"},
	} {
		t.Run(name, func(t *testing.T) {
			before := slices.Clone(tc.times)
			got := Expired(tc.times, tc.daily, tc.weekly)
			slices.SortFunc(got, func(a, b time.Time) int { return a.Compare(b) })
			var s []string
			for _, tm := range got {
				s = append(s, tm.Format("01-02 15"))
			}
			if strings.Join(s, ",") != tc.want {
				t.Fatalf("expired = %s\nwant      %s", strings.Join(s, ","), tc.want)
			}
			if !slices.Equal(before, tc.times) {
				t.Fatal("the input was reordered")
			}
		})
	}
}
