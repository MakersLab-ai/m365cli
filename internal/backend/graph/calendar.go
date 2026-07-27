package graphbackend

import (
	"context"
	"fmt"
	"net/url"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/calendar"
	"github.com/MakersLab-ai/m365cli/internal/graph"
)

type calSvc struct{ c *graph.Client }

const eventSelect = "id,subject,start,end,location,organizer,attendees,isAllDay,onlineMeetingUrl"

func (s calSvc) List(ctx context.Context, mailbox string, opts backend.CalListOpts) ([]byte, error) {
	var suffix string
	if opts.Start != "" && opts.End != "" {
		suffix = fmt.Sprintf("calendarView?startDateTime=%s&endDateTime=%s&$top=%d&$select=%s&$orderby=start/dateTime",
			url.QueryEscape(opts.Start), url.QueryEscape(opts.End), opts.Max, eventSelect)
	} else {
		suffix = fmt.Sprintf("events?$top=%d&$orderby=start/dateTime&$select=%s", opts.Max, eventSelect)
	}
	return unwrapValue(s.c.GetForMailbox(ctx, mailbox, suffix))
}

func (s calSvc) Get(ctx context.Context, mailbox, id string) ([]byte, error) {
	return s.c.GetForMailbox(ctx, mailbox, "events/"+url.PathEscape(id)+"?$select="+eventSelect+",body")
}

func (s calSvc) Create(ctx context.Context, mailbox string, ev calendar.Event) ([]byte, error) {
	payload, err := calendar.BuildEvent(ev)
	if err != nil {
		return nil, err
	}
	return s.c.PostForMailbox(ctx, mailbox, "events", payload)
}

func (s calSvc) Update(ctx context.Context, mailbox, id string, ev calendar.Event) ([]byte, error) {
	payload, err := calendar.BuildEventPatch(ev)
	if err != nil {
		return nil, err
	}
	return s.c.PatchForMailbox(ctx, mailbox, "events/"+url.PathEscape(id), payload)
}

func (s calSvc) Delete(ctx context.Context, mailbox, id string) error {
	return s.c.DeleteForMailbox(ctx, mailbox, "events/"+url.PathEscape(id))
}

func (s calSvc) FreeBusy(ctx context.Context, mailbox string, q backend.ScheduleQuery) ([]byte, error) {
	payload, err := calendar.BuildGetSchedule(q.Schedules, q.Start, q.End, q.TimeZone, q.Interval)
	if err != nil {
		return nil, err
	}
	return unwrapValue(s.c.PostForMailbox(ctx, mailbox, "calendar/getSchedule", payload))
}

func (s calSvc) FindTimes(ctx context.Context, mailbox string, q backend.FindTimesQuery) ([]byte, error) {
	payload, err := calendar.BuildFindMeetingTimes(q.Attendees, q.Start, q.End, q.TimeZone, q.Duration, q.Max)
	if err != nil {
		return nil, err
	}
	return s.c.PostForMailbox(ctx, mailbox, "findMeetingTimes", payload)
}
