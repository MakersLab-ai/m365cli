package cli

import (
	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/contacts"
)

// newContactsCmd is the contacts domain root (mailbox-scoped, same RBAC as mail).
func newContactsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "contacts",
		Short: "Mailbox contacts (app-only, scoped by allowed_mailboxes)",
	}
	c.AddCommand(newContactsListCmd(), newContactsGetCmd(), newContactsAddCmd())
	return c
}

func newContactsListCmd() *cobra.Command {
	var mailbox string
	var max int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List contacts in a mailbox",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Contacts().List(cmd.Context(), mbx, backend.ListOpts{Max: max})
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().IntVar(&max, "max", 50, "maximum number of contacts")
	return cmd
}

func newContactsGetCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "get <contact-id>",
		Short: "Get a single contact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Contacts().Get(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	return cmd
}

func newContactsAddCmd() *cobra.Command {
	var mailbox, given, surname, display string
	var emails []string
	cmd := &cobra.Command{
		Use:   "add --email <addr> [--given <n>] [--surname <n>]",
		Short: "Add a contact to a mailbox",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Contacts().Add(cmd.Context(), mbx, contacts.Contact{
				GivenName: given, Surname: surname, DisplayName: display, Emails: emails,
			})
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox to operate on (defaults to default_mailbox)")
	cmd.Flags().StringSliceVar(&emails, "email", nil, "contact email address (repeatable)")
	cmd.Flags().StringVar(&given, "given", "", "given (first) name")
	cmd.Flags().StringVar(&surname, "surname", "", "surname (last name)")
	cmd.Flags().StringVar(&display, "display", "", "display name (defaults to first email)")
	return cmd
}
