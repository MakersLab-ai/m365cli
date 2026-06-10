package watch

import "testing"

func TestParseDeltaSplitsLiveRemovedAndLinks(t *testing.T) {
	body := []byte(`{
		"@odata.nextLink": "https://graph.microsoft.com/v1.0/users/a@x.com/mailFolders/inbox/messages/delta?$skiptoken=NEXT",
		"value": [
			{"id":"m1","subject":"hi"},
			{"id":"m2","@removed":{"reason":"deleted"}}
		]
	}`)
	page, err := ParseDelta(body)
	if err != nil {
		t.Fatalf("ParseDelta: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("Items = %d, want 1 (m2 is removed)", len(page.Items))
	}
	if len(page.Removed) != 1 || page.Removed[0] != "m2" {
		t.Errorf("Removed = %v, want [m2]", page.Removed)
	}
	if page.NextLink == "" || page.DeltaLink != "" {
		t.Errorf("expected a nextLink and no deltaLink, got next=%q delta=%q", page.NextLink, page.DeltaLink)
	}
}

func TestParseDeltaReadsDeltaLinkOnFinalPage(t *testing.T) {
	body := []byte(`{"@odata.deltaLink":"https://graph.microsoft.com/v1.0/users/a@x.com/mailFolders/inbox/messages/delta?$deltatoken=DT","value":[]}`)
	page, err := ParseDelta(body)
	if err != nil {
		t.Fatalf("ParseDelta: %v", err)
	}
	if page.DeltaLink == "" || page.NextLink != "" {
		t.Errorf("expected deltaLink and no nextLink, got delta=%q next=%q", page.DeltaLink, page.NextLink)
	}
}
