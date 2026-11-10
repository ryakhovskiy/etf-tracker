package marketdata

import (
	"sort"
	"testing"
)

func TestGermanHolidays(t *testing.T) {
	for year, want := range map[int][]string{
		// Easter Sunday 2026-04-05, 2027-03-28.
		2026: {"2026-01-01", "2026-04-03", "2026-04-06", "2026-05-01", "2026-05-14", "2026-05-25", "2026-10-03", "2026-12-25", "2026-12-26"},
		2027: {"2027-01-01", "2027-03-26", "2027-03-29", "2027-05-01", "2027-05-06", "2027-05-17", "2027-10-03", "2027-12-25", "2027-12-26"},
	} {
		var got []string
		for day := range GermanHolidays(year) {
			got = append(got, day)
		}
		sort.Strings(got)
		if len(got) != len(want) {
			t.Fatal(year, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatal(year, got)
			}
		}
	}
	// Spot-check Easter against known dates.
	for year, day := range map[int]string{2019: "2019-04-21", 2024: "2024-03-31", 2025: "2025-04-20", 2038: "2038-04-25"} {
		if got := easter(year).Format("2006-01-02"); got != day {
			t.Fatal(year, got)
		}
	}
}

func TestPayDate(t *testing.T) {
	for _, c := range []struct {
		ex      string
		delay   int
		pay     string
		shifted bool
	}{
		{"2026-11-10", 0, "2026-11-10", false}, // Tuesday, no delay
		{"2026-11-10", 14, "2026-11-24", false},
		{"2026-10-02", 30, "2026-11-02", true},  // Sunday 1 November -> Monday, next month
		{"2026-03-27", 7, "2026-04-07", true},   // Good Friday, Easter weekend and Easter Monday skipped
		{"2026-04-30", 1, "2026-05-04", true},   // 1 May is a Friday holiday -> Monday
		{"2026-12-24", 1, "2026-12-28", true},   // 25/26 December, then Sunday -> Monday 28
		{"2026-12-31", 1, "2027-01-04", true},   // New Year, then the weekend: next year
		{"2026-09-03", 30, "2026-10-05", true},  // 3 October (Saturday), Sunday -> Monday
		{"2026-11-10", 90, "2027-02-08", false}, // the maximum delay
		{"bad", 5, "bad", false},
	} {
		pay, shifted := PayDate(c.ex, c.delay)
		if pay != c.pay || shifted != c.shifted {
			t.Errorf("PayDate(%s, %d) = %s %v, want %s %v", c.ex, c.delay, pay, shifted, c.pay, c.shifted)
		}
	}
}
