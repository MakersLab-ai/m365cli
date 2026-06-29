package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/calendar"
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
			data, err := client.Calendar().List(cmd.Context(), mbx, backend.CalListOpts{Start: start, End: end, Max: max})
			if err != nil {
				return err
			}
			return emitData(data)
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
			data, err := client.Calendar().Get(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			return emitData(data)
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
			data, err := client.Calendar().Create(cmd.Context(), mbx, ev)
			if err != nil {
				return err
			}
			return emitData(data)
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
			ev := calendar.Event{
				Subject: subject, Body: body, Start: start, End: end,
				TimeZone: tz, Location: location, Attendees: attendees,
			}
			data, err := client.Calendar().Update(cmd.Context(), mbx, args[0], ev)
			if err != nil {
				return err
			}
			return emitData(data)
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
			if err := client.Calendar().Delete(cmd.Context(), mbx, args[0]); err != nil {
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
			data, err := client.Calendar().FreeBusy(cmd.Context(), mbx, backend.ScheduleQuery{
				Schedules: schedules, Start: start, End: end, TimeZone: tz, Interval: interval,
			})
			if err != nil {
				return err
			}
			return emitData(data)
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
			data, err := client.Calendar().FindTimes(cmd.Context(), mbx, backend.FindTimesQuery{
				Attendees: attendees, Start: start, End: end, TimeZone: tz, Duration: duration, Max: max,
			})
			if err != nil {
				return err
			}
			return emitData(data)
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
