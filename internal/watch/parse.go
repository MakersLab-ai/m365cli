// Package watch implements mailbox delta polling and webhook forwarding for
// `m365 mail watch poll`. The core is HTTP-independent and unit-tested with fakes.
package watch

import "encoding/json"

// DeltaPage is one parsed page of a Graph messages/delta response.
type DeltaPage struct {
	Items     []json.RawMessage // live (created/updated) message objects
	Removed   []string          // ids of @removed items
	NextLink  string            // @odata.nextLink (more pages in this sync)
	DeltaLink string            // @odata.deltaLink (final cursor)
}

// ParseDelta parses a Graph messages/delta response body.
func ParseDelta(body []byte) (DeltaPage, error) {
	var raw struct {
		NextLink  string            `json:"@odata.nextLink"`
		DeltaLink string            `json:"@odata.deltaLink"`
		Value     []json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return DeltaPage{}, err
	}
	page := DeltaPage{NextLink: raw.NextLink, DeltaLink: raw.DeltaLink}
	for _, item := range raw.Value {
		var probe struct {
			ID      string          `json:"id"`
			Removed json.RawMessage `json:"@removed"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			return DeltaPage{}, err
		}
		if len(probe.Removed) > 0 {
			page.Removed = append(page.Removed, probe.ID)
			continue
		}
		page.Items = append(page.Items, item)
	}
	return page, nil
}
