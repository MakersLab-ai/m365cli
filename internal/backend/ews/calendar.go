package ewsbackend

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/calendar"
	"github.com/MakersLab-ai/m365cli/internal/ews"
)

type calSvc struct{ c *ews.Client }

// --- Graph-shaped calendar JSON (mirrors the graph backend's event projection) ---

type jsonDateTime struct {
	DateTime string `json:"dateTime"`
	TimeZone string `json:"timeZone"`
}

type jsonLocation struct {
	DisplayName string `json:"displayName"`
}

type jsonEvent struct {
	ID        string       `json:"id"`
	Subject   string       `json:"subject"`
	Start     jsonDateTime `json:"start"`
	End       jsonDateTime `json:"end"`
	Location  jsonLocation `json:"location"`
	Organizer jsonEmail    `json:"organizer"`
	Attendees []jsonEmail  `json:"attendees"`
	IsAllDay  bool         `json:"isAllDay"`
	Body      *jsonBody    `json:"body,omitempty"`
}

func eventJSONStruct(ev ews.Event) jsonEvent {
	je := jsonEvent{
		ID: ev.ID, Subject: ev.Subject,
		Start:     jsonDateTime{DateTime: ev.Start, TimeZone: "UTC"},
		End:       jsonDateTime{DateTime: ev.End, TimeZone: "UTC"},
		Location:  jsonLocation{DisplayName: ev.Location},
		Organizer: email(ev.Organizer),
		Attendees: emails(ev.Attendees),
		IsAllDay:  ev.IsAllDay,
	}
	if ev.Body != nil {
		je.Body = &jsonBody{ContentType: strings.ToLower(ev.Body.Type), Content: ev.Body.Content}
	}
	return je
}

func (s calSvc) List(ctx context.Context, mailbox string, opts backend.CalListOpts) ([]byte, error) {
	evs, err := s.c.ListEvents(ctx, mailbox, normalizeUTC(opts.Start), normalizeUTC(opts.End), opts.Max)
	if err != nil {
		return nil, err
	}
	out := make([]jsonEvent, 0, len(evs))
	for _, ev := range evs {
		out = append(out, eventJSONStruct(ev))
	}
	return json.Marshal(out)
}

func (s calSvc) Get(ctx context.Context, mailbox, id string) ([]byte, error) {
	ev, err := s.c.GetEvent(ctx, mailbox, id)
	if err != nil {
		return nil, err
	}
	return json.Marshal(eventJSONStruct(ev))
}

func (s calSvc) Create(ctx context.Context, mailbox string, ev calendar.Event) ([]byte, error) {
	id, err := s.c.CreateEvent(ctx, mailbox, eventInput(ev))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"id": id})
}

func (s calSvc) Update(ctx context.Context, mailbox, id string, ev calendar.Event) ([]byte, error) {
	newID, err := s.c.UpdateEvent(ctx, mailbox, id, eventInput(ev))
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"id": newID})
}

func (s calSvc) Delete(ctx context.Context, mailbox, id string) error {
	return s.c.DeleteEvent(ctx, mailbox, id)
}

// FreeBusy and FindTimes require the EWS GetUserAvailability availability
// service (a full timezone-bias block + suggestions polymorphism). Deferred —
// the mail+calendar-CRUD path covers the agent use-case.
func (s calSvc) FreeBusy(context.Context, string, backend.ScheduleQuery) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (s calSvc) FindTimes(context.Context, string, backend.FindTimesQuery) ([]byte, error) {
	return nil, backend.ErrUnsupported
}

// eventInput maps the neutral calendar input onto the EWS transport input,
// treating wall-clock times as UTC (EWS requires an explicit zone).
func eventInput(ev calendar.Event) ews.EventInput {
	return ews.EventInput{
		Subject: ev.Subject, Body: ev.Body,
		Start: normalizeUTC(ev.Start), End: normalizeUTC(ev.End),
		Location: ev.Location, Attendees: ev.Attendees,
	}
}

// normalizeUTC appends a "Z" when a datetime carries no zone designator, so EWS
// interprets it deterministically as UTC.
func normalizeUTC(t string) string {
	if t == "" || strings.HasSuffix(t, "Z") {
		return t
	}
	// A timezone offset (+hh:mm / -hh:mm) lives in the time part, after the "T".
	if i := strings.IndexByte(t, 'T'); i >= 0 {
		if timePart := t[i+1:]; strings.ContainsAny(timePart, "+-") {
			return t
		}
	}
	return t + "Z"
}
