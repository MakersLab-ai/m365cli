// Package calendar builds Microsoft Graph calendar payloads (events, free/busy
// queries). It is HTTP-independent; the CLI layer pairs it with the scoped Graph
// client. Times are passed through as Graph dateTime strings with a timeZone.
package calendar

import (
	"encoding/json"
	"fmt"
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
	if e.Start == "" || e.End == "" {
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
