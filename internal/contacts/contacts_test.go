package contacts

import (
	"encoding/json"
	"testing"
)

func TestBuildContactShape(t *testing.T) {
	payload, err := BuildContact(Contact{
		GivenName:   "Ada",
		Surname:     "Lovelace",
		DisplayName: "Ada Lovelace",
		Emails:      []string{"ada@x.com", "ada.work@x.com"},
	})
	if err != nil {
		t.Fatalf("BuildContact: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if m["givenName"] != "Ada" || m["surname"] != "Lovelace" || m["displayName"] != "Ada Lovelace" {
		t.Errorf("name fields = %v", m)
	}
	emails, _ := m["emailAddresses"].([]any)
	if len(emails) != 2 {
		t.Fatalf("emailAddresses = %v", m["emailAddresses"])
	}
	first, _ := emails[0].(map[string]any)
	if first["address"] != "ada@x.com" {
		t.Errorf("first email = %v", first["address"])
	}
}

func TestBuildContactDefaultsDisplayNameFromEmail(t *testing.T) {
	payload, err := BuildContact(Contact{Emails: []string{"solo@x.com"}})
	if err != nil {
		t.Fatalf("BuildContact: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(payload, &m)
	if m["displayName"] != "solo@x.com" {
		t.Errorf("displayName = %v, want fallback to email", m["displayName"])
	}
}

func TestBuildContactRequiresNameOrEmail(t *testing.T) {
	if _, err := BuildContact(Contact{}); err == nil {
		t.Error("BuildContact must require at least a name or an email")
	}
}
