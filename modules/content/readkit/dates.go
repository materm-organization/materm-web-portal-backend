package readkit

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Vientiane is the time zone in which the site shows dates (doc/08 section 2.3.1, BR-32). Laos has no
// daylight saving time and is UTC+7 all year, so a fixed zone is exact and does not need the tz database.
var Vientiane = time.FixedZone("Asia/Vientiane", 7*60*60)

// DateFormats is date_formats of a locale (v_public_locales): `short` is a pattern with the placeholders
// {day} (two digits), {month} (the abbreviation from Months, or two digits when Months is not a list of 12)
// and {year} (four digits); Months holds the twelve month names.
type DateFormats struct {
	Short  string   `json:"short"`
	Months []string `json:"months"`
}

// ParseDateFormats reads the jsonb of v_public_locales.date_formats. Anything unusable yields the zero value,
// which formats as an ISO date.
func ParseDateFormats(raw []byte) DateFormats {
	var f DateFormats
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &f)
	}
	return f
}

// Format renders t as a calendar date in Asia/Vientiane. The same function must format every date of a
// response (post cards, blog_strip) so that the site shows one style.
func (f DateFormats) Format(t time.Time) string {
	t = t.In(Vientiane)
	pattern := strings.TrimSpace(f.Short)
	if pattern == "" {
		pattern = "{year}-{month}-{day}"
	}
	month := fmt.Sprintf("%02d", int(t.Month()))
	if len(f.Months) == 12 && f.Months[t.Month()-1] != "" {
		month = f.Months[t.Month()-1]
	}
	return strings.NewReplacer(
		"{day}", fmt.Sprintf("%02d", t.Day()),
		"{month}", month,
		"{year}", fmt.Sprintf("%04d", t.Year()),
	).Replace(pattern)
}

// ISO renders the machine-readable form that goes next to the formatted date (<time dateTime>): RFC 3339 in UTC.
func ISO(t time.Time) string { return t.UTC().Format(time.RFC3339) }
