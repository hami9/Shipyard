package backup

import (
	"slices"
	"time"
)

// Expired picks the backups rotation removes (ADR-0006: 14 dailies and 8
// weeklies by default). A backup stays if it is the newest one of its day,
// among the last daily days that have a backup, or the newest one of its
// ISO week, among the last weekly such weeks. The newest backup always
// stays. Times are compared in UTC.
func Expired(times []time.Time, daily, weekly int) []time.Time {
	sorted := slices.Clone(times)
	slices.SortFunc(sorted, func(a, b time.Time) int { return b.Compare(a) }) // newest first
	days, weeks := map[string]bool{}, map[[2]int]bool{}
	var expired []time.Time
	for i, t := range sorted {
		t = t.UTC()
		day := t.Format(time.DateOnly)
		year, week := t.ISOWeek()
		keep := i == 0
		if !days[day] && len(days) < daily {
			keep = true
		}
		if !weeks[[2]int{year, week}] && len(weeks) < weekly {
			keep = true
		}
		// Only the newest backup of a day or week can hold its slot.
		if len(days) < daily {
			days[day] = true
		}
		if len(weeks) < weekly {
			weeks[[2]int{year, week}] = true
		}
		if !keep {
			expired = append(expired, sorted[i])
		}
	}
	return expired
}
