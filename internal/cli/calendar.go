package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/calendar"
	"github.com/MakersLab-ai/m365cli/internal/graph"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

// newCalendarCmd is the calendar domain root (mailbox-scoped, same RBAC as mail).
func newCalendarCmd() *cobra.Command {
	cal := &cobra.Command{
		Use:   "calendar",
		Short: "Calendar operations (app-only, scoped by allowed_mailboxes)",
	}
	cal.AddCommand(
		newCalListCmd(), newCalGetCmd(), newCalCreateCmd(), newCalUpdateCmd(),
		newCalDeleteCmd(), newCalFreeBusyCmd(), newCalFindTimesCmd(),
	)
	return cal
}

const eventSelect = "id,subject,start,end,location,organizer,attendees,isAllDay,onlineMeetingUrl"

func newCalListCmd() *cobra.Command {
	var mailbox, start, end string
	var max int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List events (or a time window with --start/--end via calendarView)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			var suffix string
			if start != "" && end != "" {
				suffix = fmt.Sprintf("calendarView?startDateTime=%s&endDateTime=%s&$top=%d&$select=%s&$orderby=start/dateTime",
					url.QueryEscape(start), url.QueryEscape(end), max, eventSelect)
			} else {
				suffix = fmt.Sprintf("events?$top=%d&$orderby=start/dateTime&$select=%s", max, eventSelect)
			}
			return emitGraphValue(cmd.Context(), client, mbx, suffix)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().StringVar(&start, "start", "", "window start (e.g. 2026-06-10T00:00:00) — requires --end")
	cmd.Flags().StringVar(&end, "end", "", "window end — requires --start")
	cmd.Flags().IntVar(&max, "max", 25, "maximum number of events")
	return cmd
}

func newCalGetCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "get <event-id>",
		Short: "Get a single event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			body, err := client.GetForMailbox(cmd.Context(), mbx, "events/"+url.PathEscape(args[0])+"?$select="+eventSelect+",body")
			if err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, json.RawMessage(body))
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	return cmd
}

func newCalCreateCmd() *cobra.Command {
	var mailbox, subject, start, end, tz, location, bodyFile string
	var attendees []string
	cmd := &cobra.Command{
		Use:   "create --subject <s> --start <t> --end <t>",
		Short: "Create an event",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			ev, err := eventFromFlags(subject, start, end, tz, location, bodyFile, attendees)
			if err != nil {
				return err
			}
			payload, err := calendar.BuildEvent(ev)
			if err != nil {
				return err
			}
			body, err := client.PostForMailbox(cmd.Context(), mbx, "events", payload)
			if err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, json.RawMessage(body))
		},
	}
	addEventFlags(cmd, &mailbox, &subject, &start, &end, &tz, &location, &bodyFile, &attendees)
	return cmd
}

func newCalUpdateCmd() *cobra.Command {
	var mailbox, subject, start, end, tz, location, bodyFile string
	var attendees []string
	cmd := &cobra.Command{
		Use:   "update <event-id>",
		Short: "Update fields of an event (only provided fields change)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			body := ""
			if bodyFile != "" {
				b, err := os.ReadFile(bodyFile)
				if err != nil {
					return fmt.Errorf("read --body-file: %w", err)
				}
				body = string(b)
			}
			payload, err := calendar.BuildEventPatch(calendar.Event{
				Subject: subject, Body: body, Start: start, End: end,
				TimeZone: tz, Location: location, Attendees: attendees,
			})
			if err != nil {
				return err
			}
			resp, err := client.PatchForMailbox(cmd.Context(), mbx, "events/"+url.PathEscape(args[0]), payload)
			if err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, json.RawMessage(resp))
		},
	}
	addEventFlags(cmd, &mailbox, &subject, &start, &end, &tz, &location, &bodyFile, &attendees)
	return cmd
}

func newCalDeleteCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "delete <event-id>",
		Short: "Delete an event",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			if err := client.DeleteForMailbox(cmd.Context(), mbx, "events/"+url.PathEscape(args[0])); err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, map[string]any{"deleted": true, "id": args[0], "mailbox": mbx})
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	return cmd
}

func newCalFreeBusyCmd() *cobra.Command {
	var mailbox, start, end, tz string
	var schedules []string
	var interval int
	cmd := &cobra.Command{
		Use:   "freebusy --schedule <addr> --start <t> --end <t>",
		Short: "Query free/busy for one or more schedules",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			payload, err := calendar.BuildGetSchedule(schedules, start, end, tz, interval)
			if err != nil {
				return err
			}
			return postAndEmitValue(cmd.Context(), client, mbx, "calendar/getSchedule", payload)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox whose calendar issues the query (defaults to default_mailbox)")
	cmd.Flags().StringSliceVar(&schedules, "schedule", nil, "schedule address to query (repeatable)")
	cmd.Flags().StringVar(&start, "start", "", "window start (e.g. 2026-06-10T09:00:00)")
	cmd.Flags().StringVar(&end, "end", "", "window end")
	cmd.Flags().StringVar(&tz, "timezone", "", "time zone (default UTC)")
	cmd.Flags().IntVar(&interval, "interval", 30, "availability view interval in minutes")
	return cmd
}

func newCalFindTimesCmd() *cobra.Command {
	var mailbox, start, end, tz, duration string
	var attendees []string
	var max int
	cmd := &cobra.Command{
		Use:   "find-times --attendee <addr> --duration <PT30M>",
		Short: "Suggest meeting times for attendees",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			payload, err := calendar.BuildFindMeetingTimes(attendees, start, end, tz, duration, max)
			if err != nil {
				return err
			}
			body, err := client.PostForMailbox(cmd.Context(), mbx, "findMeetingTimes", payload)
			if err != nil {
				return err
			}
			return output.WriteJSON(os.Stdout, json.RawMessage(body))
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "organizing mailbox (defaults to default_mailbox)")
	cmd.Flags().StringSliceVar(&attendees, "attendee", nil, "attendee address (repeatable)")
	cmd.Flags().StringVar(&duration, "duration", "PT30M", "meeting duration (ISO 8601, e.g. PT30M)")
	cmd.Flags().StringVar(&start, "start", "", "earliest start of the search window")
	cmd.Flags().StringVar(&end, "end", "", "latest end of the search window")
	cmd.Flags().StringVar(&tz, "timezone", "", "time zone (default UTC)")
	cmd.Flags().IntVar(&max, "max", 20, "maximum number of suggestions")
	return cmd
}

// --- shared event helpers ---

func addEventFlags(cmd *cobra.Command, mailbox, subject, start, end, tz, location, bodyFile *string, attendees *[]string) {
	cmd.Flags().StringVar(mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().StringVar(subject, "subject", "", "event subject")
	cmd.Flags().StringVar(start, "start", "", "start time, e.g. 2026-06-10T10:00:00")
	cmd.Flags().StringVar(end, "end", "", "end time, e.g. 2026-06-10T10:30:00")
	cmd.Flags().StringVar(tz, "timezone", "", "time zone (default UTC)")
	cmd.Flags().StringVar(location, "location", "", "event location")
	cmd.Flags().StringVar(bodyFile, "body-file", "", "path to a file with the event body")
	cmd.Flags().StringSliceVar(attendees, "attendee", nil, "attendee address (repeatable)")
}

func eventFromFlags(subject, start, end, tz, location, bodyFile string, attendees []string) (calendar.Event, error) {
	body := ""
	if bodyFile != "" {
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			return calendar.Event{}, fmt.Errorf("read --body-file: %w", err)
		}
		body = string(b)
	}
	return calendar.Event{
		Subject: subject, Body: body, Start: start, End: end,
		TimeZone: tz, Location: location, Attendees: attendees,
	}, nil
}

func postAndEmitValue(ctx context.Context, client *graph.Client, mbx, suffix string, payload []byte) error {
	body, err := client.PostForMailbox(ctx, mbx, suffix, payload)
	if err != nil {
		return err
	}
	var page struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return fmt.Errorf("parse Graph response: %w", err)
	}
	return output.WriteJSON(os.Stdout, json.RawMessage(page.Value))
}
