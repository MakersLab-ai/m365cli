// Package calendar builds Microsoft Graph calendar payloads (events, free/busy
// queries). It is HTTP-independent; the CLI layer pairs it with the scoped Graph
// client. Times are passed through as Graph dateTime strings with a timeZone.
package calendar

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const defaultTimeZone = "UTC"

// Event is a calendar event to create or update.
type Event struct {
	Subject   string
	Body      string
	Start     string // Graph dateTime, e.g. "2026-06-10T10:00:00"
	End       string
	TimeZone  string // IANA/Windows zone; defaults to UTC
	Location  string
	Attendees []string
	// AllDay sets Graph's isAllDay. nil leaves it untouched (update) / timed
	// (create). For true, Start/End are dates — see allDayRange.
	AllDay *bool
}

type dateTimeZone struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type attendee struct {
	EmailAddress struct {
		Address string `json:"address"`
	} `json:"emailAddress"`
	Type string `json:"type"`
}

// BuildEvent renders a Graph event object for POST /events or PATCH /events/{id}.
func BuildEvent(e Event) ([]byte, error) {
	allDay := e.AllDay != nil && *e.AllDay
	if allDay {
		start, end, err := allDayRange(e.Start, e.End)
		if err != nil {
			return nil, err
		}
		e.Start, e.End = start, end
	} else if e.Start == "" || e.End == "" {
		return nil, fmt.Errorf("event requires both --start and --end")
	}
	tz := e.TimeZone
	if tz == "" {
		tz = defaultTimeZone
	}

	out := map[string]any{
		"subject": e.Subject,
		"start":   dateTimeZone{DateTime: e.Start, TimeZone: tz},
		"end":     dateTimeZone{DateTime: e.End, TimeZone: tz},
	}
	if allDay {
		out["isAllDay"] = true
	}
	if e.Body != "" {
		out["body"] = map[string]string{"contentType": "Text", "content": e.Body}
	}
	if e.Location != "" {
		out["location"] = map[string]string{"displayName": e.Location}
	}
	if len(e.Attendees) > 0 {
		out["attendees"] = buildAttendees(e.Attendees)
	}
	return json.Marshal(out)
}

// BuildEventPatch renders a partial event for PATCH /events/{id}, including only
// the fields that were provided. Start/End are paired with the time zone.
func BuildEventPatch(e Event) ([]byte, error) {
	tz := e.TimeZone
	if tz == "" {
		tz = defaultTimeZone
	}
	out := map[string]any{}
	if e.AllDay != nil {
		if *e.AllDay {
			// Graph rejects isAllDay=true unless start and end are midnight, so
			// the switch must carry both — derived from the given dates.
			if e.Start == "" {
				return nil, fmt.Errorf("--all-day on update requires --start (the day; --end optional)")
			}
			start, end, err := allDayRange(e.Start, e.End)
			if err != nil {
				return nil, err
			}
			e.Start, e.End = start, end
		}
		out["isAllDay"] = *e.AllDay
	}
	if e.Subject != "" {
		out["subject"] = e.Subject
	}
	if e.Body != "" {
		out["body"] = map[string]string{"contentType": "Text", "content": e.Body}
	}
	if e.Start != "" {
		out["start"] = dateTimeZone{DateTime: e.Start, TimeZone: tz}
	}
	if e.End != "" {
		out["end"] = dateTimeZone{DateTime: e.End, TimeZone: tz}
	}
	if e.Location != "" {
		out["location"] = map[string]string{"displayName": e.Location}
	}
	if len(e.Attendees) > 0 {
		out["attendees"] = buildAttendees(e.Attendees)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("nothing to update: provide at least one field")
	}
	return json.Marshal(out)
}

const (
	dateLayout     = "2006-01-02"
	midnightSuffix = "T00:00:00"
)

// allDayRange turns all-day input into Graph's midnight-to-midnight range.
// Start is a date (2026-06-10) or a midnight dateTime. End is optional (one
// day); a date-only End is the inclusive last day (human "10. bis 12."), a
// midnight dateTime End is Graph's exclusive end and passes through.
func allDayRange(start, end string) (string, string, error) {
	if start == "" {
		return "", "", fmt.Errorf("all-day event requires --start (a date, e.g. 2026-06-10)")
	}
	s, _, err := parseAllDay("--start", start)
	if err != nil {
		return "", "", err
	}
	e := s.AddDate(0, 0, 1)
	if end != "" {
		d, dateOnly, err := parseAllDay("--end", end)
		if err != nil {
			return "", "", err
		}
		if dateOnly {
			d = d.AddDate(0, 0, 1)
		}
		if !d.After(s) {
			return "", "", fmt.Errorf("all-day --end must be after --start")
		}
		e = d
	}
	return s.Format(dateLayout) + midnightSuffix, e.Format(dateLayout) + midnightSuffix, nil
}

// parseAllDay accepts YYYY-MM-DD or YYYY-MM-DDT00:00:00[.000…] and reports
// whether the value was date-only.
func parseAllDay(flag, v string) (time.Time, bool, error) {
	date, rest, hasTime := strings.Cut(v, "T")
	d, err := time.Parse(dateLayout, date)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("all-day %s must be a date like 2026-06-10, got %q", flag, v)
	}
	if !hasTime {
		return d, true, nil
	}
	if strings.Trim(strings.ReplaceAll(rest, ":", ""), "0.") != "" || !strings.HasPrefix(rest, "00:00") {
		return time.Time{}, false, fmt.Errorf("all-day %s must be midnight (all-day events have no time of day), got %q", flag, v)
	}
	return d, false, nil
}

// BuildFindMeetingTimes renders the POST /findMeetingTimes payload.
func BuildFindMeetingTimes(attendees []string, start, end, timeZone, duration string, maxCandidates int) ([]byte, error) {
	if len(attendees) == 0 {
		return nil, fmt.Errorf("findMeetingTimes requires at least one --attendee")
	}
	if duration == "" {
		return nil, fmt.Errorf("findMeetingTimes requires a --duration (ISO 8601, e.g. PT30M)")
	}
	tz := timeZone
	if tz == "" {
		tz = defaultTimeZone
	}
	if maxCandidates <= 0 {
		maxCandidates = 20
	}
	out := map[string]any{
		"attendees":       buildAttendees(attendees),
		"meetingDuration": duration,
		"maxCandidates":   maxCandidates,
	}
	if start != "" && end != "" {
		out["timeConstraint"] = map[string]any{
			"timeSlots": []any{map[string]any{
				"start": dateTimeZone{DateTime: start, TimeZone: tz},
				"end":   dateTimeZone{DateTime: end, TimeZone: tz},
			}},
		}
	}
	return json.Marshal(out)
}

func buildAttendees(addrs []string) []attendee {
	attendees := make([]attendee, 0, len(addrs))
	for _, addr := range addrs {
		var a attendee
		a.EmailAddress.Address = addr
		a.Type = "required"
		attendees = append(attendees, a)
	}
	return attendees
}

// BuildGetSchedule renders the POST /calendar/getSchedule payload for free/busy.
func BuildGetSchedule(schedules []string, start, end, timeZone string, intervalMin int) ([]byte, error) {
	if len(schedules) == 0 {
		return nil, fmt.Errorf("getSchedule requires at least one --schedule")
	}
	if start == "" || end == "" {
		return nil, fmt.Errorf("getSchedule requires --start and --end")
	}
	tz := timeZone
	if tz == "" {
		tz = defaultTimeZone
	}
	if intervalMin <= 0 {
		intervalMin = 30
	}
	return json.Marshal(map[string]any{
		"schedules":                schedules,
		"startTime":                dateTimeZone{DateTime: start, TimeZone: tz},
		"endTime":                  dateTimeZone{DateTime: end, TimeZone: tz},
		"availabilityViewInterval": intervalMin,
	})
}
