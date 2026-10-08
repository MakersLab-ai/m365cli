package calendar

import (
	"encoding/json"
	"testing"
)

func TestBuildEventShape(t *testing.T) {
	payload, err := BuildEvent(Event{
		Subject:   "Sync",
		Body:      "agenda",
		Start:     "2026-06-10T10:00:00",
		End:       "2026-06-10T10:30:00",
		TimeZone:  "Europe/Vienna",
		Location:  "Room 1",
		Attendees: []string{"a@x.com", "b@x.com"},
	})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if m["subject"] != "Sync" {
		t.Errorf("subject = %v", m["subject"])
	}
	start, _ := m["start"].(map[string]any)
	if start["dateTime"] != "2026-06-10T10:00:00" || start["timeZone"] != "Europe/Vienna" {
		t.Errorf("start = %v", start)
	}
	loc, _ := m["location"].(map[string]any)
	if loc["displayName"] != "Room 1" {
		t.Errorf("location = %v", loc)
	}
	att, _ := m["attendees"].([]any)
	if len(att) != 2 {
		t.Fatalf("attendees = %v", m["attendees"])
	}
	a0, _ := att[0].(map[string]any)
	if a0["type"] != "required" {
		t.Errorf("attendee type = %v, want required", a0["type"])
	}
	addr, _ := a0["emailAddress"].(map[string]any)
	if addr["address"] != "a@x.com" {
		t.Errorf("attendee address = %v", addr["address"])
	}
}

func TestBuildEventDefaultsTimeZoneToUTC(t *testing.T) {
	payload, err := BuildEvent(Event{Subject: "S", Start: "2026-06-10T10:00:00", End: "2026-06-10T10:30:00"})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	start, _ := m["start"].(map[string]any)
	if start["timeZone"] != "UTC" {
		t.Errorf("default timeZone = %v, want UTC", start["timeZone"])
	}
}

func TestBuildEventRequiresStartAndEnd(t *testing.T) {
	if _, err := BuildEvent(Event{Subject: "S", End: "2026-06-10T10:30:00"}); err == nil {
		t.Error("BuildEvent must require start")
	}
	if _, err := BuildEvent(Event{Subject: "S", Start: "2026-06-10T10:00:00"}); err == nil {
		t.Error("BuildEvent must require end")
	}
}

func TestBuildGetScheduleShape(t *testing.T) {
	payload, err := BuildGetSchedule([]string{"a@x.com"}, "2026-06-10T09:00:00", "2026-06-10T17:00:00", "", 30)
	if err != nil {
		t.Fatalf("BuildGetSchedule: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	sched, _ := m["schedules"].([]any)
	if len(sched) != 1 || sched[0] != "a@x.com" {
		t.Errorf("schedules = %v", m["schedules"])
	}
	st, _ := m["startTime"].(map[string]any)
	if st["dateTime"] != "2026-06-10T09:00:00" || st["timeZone"] != "UTC" {
		t.Errorf("startTime = %v", st)
	}
	if m["availabilityViewInterval"] != float64(30) {
		t.Errorf("availabilityViewInterval = %v", m["availabilityViewInterval"])
	}
}

func TestBuildGetScheduleRequiresInputs(t *testing.T) {
	if _, err := BuildGetSchedule(nil, "2026-06-10T09:00:00", "2026-06-10T17:00:00", "", 30); err == nil {
		t.Error("BuildGetSchedule must require at least one schedule")
	}
	if _, err := BuildGetSchedule([]string{"a@x.com"}, "", "2026-06-10T17:00:00", "", 30); err == nil {
		t.Error("BuildGetSchedule must require start")
	}
}

func TestBuildEventPatchOnlyIncludesProvidedFields(t *testing.T) {
	payload, err := BuildEventPatch(Event{Subject: "New subject"})
	if err != nil {
		t.Fatalf("BuildEventPatch: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if m["subject"] != "New subject" {
		t.Errorf("subject = %v", m["subject"])
	}
	if _, ok := m["start"]; ok {
		t.Error("patch must not include start when it was not provided")
	}
	if _, ok := m["end"]; ok {
		t.Error("patch must not include end when it was not provided")
	}
}

func TestBuildEventPatchRequiresAtLeastOneField(t *testing.T) {
	if _, err := BuildEventPatch(Event{}); err == nil {
		t.Error("BuildEventPatch must error when there is nothing to update")
	}
}

func TestBuildEventPatchStartRequiresTimeZonePairing(t *testing.T) {
	payload, err := BuildEventPatch(Event{Start: "2026-06-10T11:00:00", End: "2026-06-10T11:30:00", TimeZone: "Europe/Vienna"})
	if err != nil {
		t.Fatalf("BuildEventPatch: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	start, _ := m["start"].(map[string]any)
	if start["dateTime"] != "2026-06-10T11:00:00" || start["timeZone"] != "Europe/Vienna" {
		t.Errorf("start = %v", start)
	}
}

func TestBuildFindMeetingTimesShape(t *testing.T) {
	payload, err := BuildFindMeetingTimes([]string{"a@x.com"}, "2026-06-10T09:00:00", "2026-06-10T17:00:00", "UTC", "PT30M", 5)
	if err != nil {
		t.Fatalf("BuildFindMeetingTimes: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if m["meetingDuration"] != "PT30M" {
		t.Errorf("meetingDuration = %v", m["meetingDuration"])
	}
	att, _ := m["attendees"].([]any)
	if len(att) != 1 {
		t.Fatalf("attendees = %v", m["attendees"])
	}
	tc, _ := m["timeConstraint"].(map[string]any)
	slots, _ := tc["timeSlots"].([]any)
	if len(slots) != 1 {
		t.Errorf("timeSlots = %v", tc["timeSlots"])
	}
}

func TestBuildFindMeetingTimesRequiresAttendeesAndDuration(t *testing.T) {
	if _, err := BuildFindMeetingTimes(nil, "", "", "", "PT30M", 5); err == nil {
		t.Error("findMeetingTimes must require attendees")
	}
	if _, err := BuildFindMeetingTimes([]string{"a@x.com"}, "", "", "", "", 5); err == nil {
		t.Error("findMeetingTimes must require a meeting duration")
	}
}

func boolPtr(b bool) *bool { return &b }

func decode(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	return m
}

func dt(m map[string]any, key string) (string, string) {
	v, _ := m[key].(map[string]any)
	d, _ := v["dateTime"].(string)
	z, _ := v["timeZone"].(string)
	return d, z
}

func TestBuildEventAllDaySingleDayDefaultsEndToNextMidnight(t *testing.T) {
	payload, err := BuildEvent(Event{Subject: "Urlaub", Start: "2026-06-10", TimeZone: "Europe/Vienna", AllDay: boolPtr(true)})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	m := decode(t, payload)
	if m["isAllDay"] != true {
		t.Errorf("isAllDay = %v", m["isAllDay"])
	}
	if d, z := dt(m, "start"); d != "2026-06-10T00:00:00" || z != "Europe/Vienna" {
		t.Errorf("start = %s %s", d, z)
	}
	if d, z := dt(m, "end"); d != "2026-06-11T00:00:00" || z != "Europe/Vienna" {
		t.Errorf("end = %s %s", d, z)
	}
}

func TestBuildEventAllDayDateOnlyEndIsInclusiveLastDay(t *testing.T) {
	payload, err := BuildEvent(Event{Subject: "Messe", Start: "2026-06-10", End: "2026-06-12", AllDay: boolPtr(true)})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	m := decode(t, payload)
	if d, _ := dt(m, "end"); d != "2026-06-13T00:00:00" {
		t.Errorf("end = %s, want exclusive midnight after the last day", d)
	}
}

func TestBuildEventAllDayMidnightDateTimeEndIsExclusive(t *testing.T) {
	payload, err := BuildEvent(Event{Subject: "Messe", Start: "2026-06-10T00:00:00", End: "2026-06-12T00:00:00", AllDay: boolPtr(true)})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	m := decode(t, payload)
	if d, _ := dt(m, "start"); d != "2026-06-10T00:00:00" {
		t.Errorf("start = %s", d)
	}
	if d, _ := dt(m, "end"); d != "2026-06-12T00:00:00" {
		t.Errorf("end = %s, Graph-style midnight end must pass through", d)
	}
}

func TestBuildEventAllDayRejectsNonMidnightOrBackwards(t *testing.T) {
	cases := []Event{
		{Start: "2026-06-10T10:00:00", AllDay: boolPtr(true)},
		{Start: "2026-06-10", End: "2026-06-10T12:00:00", AllDay: boolPtr(true)},
		{Start: "2026-06-10", End: "2026-06-09", AllDay: boolPtr(true)},
		{Start: "2026-06-10T00:00:00", End: "2026-06-10T00:00:00", AllDay: boolPtr(true)},
		{Start: "10.06.2026", AllDay: boolPtr(true)},
		{AllDay: boolPtr(true)},
	}
	for _, c := range cases {
		if _, err := BuildEvent(c); err == nil {
			t.Errorf("BuildEvent(%+v): expected error", c)
		}
	}
}

func TestBuildEventTimedHasNoIsAllDay(t *testing.T) {
	payload, err := BuildEvent(Event{Start: "2026-06-10T10:00:00", End: "2026-06-10T11:00:00"})
	if err != nil {
		t.Fatalf("BuildEvent: %v", err)
	}
	if _, ok := decode(t, payload)["isAllDay"]; ok {
		t.Error("timed event must not carry isAllDay")
	}
}

func TestBuildEventPatchSwitchToAllDay(t *testing.T) {
	payload, err := BuildEventPatch(Event{Start: "2026-06-10", AllDay: boolPtr(true)})
	if err != nil {
		t.Fatalf("BuildEventPatch: %v", err)
	}
	m := decode(t, payload)
	if m["isAllDay"] != true {
		t.Errorf("isAllDay = %v", m["isAllDay"])
	}
	if d, _ := dt(m, "start"); d != "2026-06-10T00:00:00" {
		t.Errorf("start = %s", d)
	}
	if d, _ := dt(m, "end"); d != "2026-06-11T00:00:00" {
		t.Errorf("end = %s", d)
	}
}

func TestBuildEventPatchAllDayRequiresStart(t *testing.T) {
	if _, err := BuildEventPatch(Event{AllDay: boolPtr(true)}); err == nil {
		t.Error("switching to all-day without --start must fail (Graph needs midnight start/end in the same PATCH)")
	}
}

func TestBuildEventPatchSwitchToTimed(t *testing.T) {
	payload, err := BuildEventPatch(Event{Start: "2026-06-10T09:00:00", End: "2026-06-10T10:00:00", AllDay: boolPtr(false)})
	if err != nil {
		t.Fatalf("BuildEventPatch: %v", err)
	}
	m := decode(t, payload)
	if v, ok := m["isAllDay"]; !ok || v != false {
		t.Errorf("isAllDay = %v (present=%v), want explicit false", v, ok)
	}
	if d, _ := dt(m, "start"); d != "2026-06-10T09:00:00" {
		t.Errorf("start = %s", d)
	}
}
