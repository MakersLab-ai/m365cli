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
