// Package contacts builds Microsoft Graph contact payloads. Contacts live in a
// mailbox, so the CLI pairs this with the mailbox-scoped Graph client (same RBAC
// scoping as mail/calendar).
package contacts

import (
	"encoding/json"
	"fmt"
)

// Contact is a mailbox contact to create.
type Contact struct {
	GivenName   string
	Surname     string
	DisplayName string
	Emails      []string
}

type emailAddress struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

// BuildContact renders a Graph contact object for POST /contacts. It requires at
// least a name or an email, and defaults displayName to the first email.
func BuildContact(c Contact) ([]byte, error) {
	if c.GivenName == "" && c.Surname == "" && c.DisplayName == "" && len(c.Emails) == 0 {
		return nil, fmt.Errorf("contact requires at least a name or an email")
	}
	display := c.DisplayName
	if display == "" {
		if len(c.Emails) > 0 {
			display = c.Emails[0]
		} else {
			display = joinName(c.GivenName, c.Surname)
		}
	}

	out := map[string]any{"displayName": display}
	if c.GivenName != "" {
		out["givenName"] = c.GivenName
	}
	if c.Surname != "" {
		out["surname"] = c.Surname
	}
	if len(c.Emails) > 0 {
		addrs := make([]emailAddress, 0, len(c.Emails))
		for _, e := range c.Emails {
			addrs = append(addrs, emailAddress{Address: e, Name: display})
		}
		out["emailAddresses"] = addrs
	}
	return json.Marshal(out)
}

func joinName(given, surname string) string {
	switch {
	case given != "" && surname != "":
		return given + " " + surname
	case given != "":
		return given
	default:
		return surname
	}
}
