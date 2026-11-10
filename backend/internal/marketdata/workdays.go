package marketdata

import "time"

// MaxPayDelayDays is the largest pay-date delay a user can configure.
const MaxPayDelayDays = 90

// easter returns Easter Sunday of year (Gregorian calendar, anonymous algorithm).
func easter(year int) time.Time {
	a := year % 19
	b, c := year/100, year%100
	d, e := b/4, b%4
	f := (b + 8) / 25
	g := (b - f + 1) / 3
	h := (19*a + b - d - g + 15) % 30
	i, k := c/4, c%4
	l := (32 + 2*e + 2*i - h - k) % 7
	m := (a + 11*h + 22*l) / 451
	month := (h + l - 7*m + 114) / 31
	day := (h+l-7*m+114)%31 + 1
	return time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
}

// GermanHolidays returns the nine nationwide public holidays of year in
// Germany (YYYY-MM-DD -> name). Regional holidays are not included.
func GermanHolidays(year int) map[string]string {
	fixed := func(m time.Month, d int) string {
		return time.Date(year, m, d, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	}
	e := easter(year)
	rel := func(days int) string { return e.AddDate(0, 0, days).Format("2006-01-02") }
	return map[string]string{
		fixed(time.January, 1):   "Neujahr",
		rel(-2):                  "Karfreitag",
		rel(1):                   "Ostermontag",
		fixed(time.May, 1):       "Tag der Arbeit",
		rel(39):                  "Christi Himmelfahrt",
		rel(50):                  "Pfingstmontag",
		fixed(time.October, 3):   "Tag der Deutschen Einheit",
		fixed(time.December, 25): "1. Weihnachtstag",
		fixed(time.December, 26): "2. Weihnachtstag",
	}
}

// IsGermanWorkday reports whether t is a weekday that is not a nationwide
// German public holiday.
func IsGermanWorkday(t time.Time) bool {
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		return false
	}
	_, holiday := GermanHolidays(t.Year())[t.Format("2006-01-02")]
	return !holiday
}

// PayDate estimates the payment day of a distribution: the ex-date plus delay
// calendar days, moved forward to the next German working day. shifted reports
// whether it was moved. An unreadable ex-date is returned unchanged.
func PayDate(exDate string, delay int) (pay string, shifted bool) {
	t, err := time.Parse("2006-01-02", exDate)
	if err != nil {
		return exDate, false
	}
	t = t.AddDate(0, 0, delay)
	for !IsGermanWorkday(t) {
		t, shifted = t.AddDate(0, 0, 1), true
	}
	return t.Format("2006-01-02"), shifted
}
