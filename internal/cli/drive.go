package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/output"
)

// newDriveCmd is the OneDrive domain root. A user's drive lives under the user,
// so it is scoped by allowed_mailboxes (same user allowlist as mail).
func newDriveCmd() *cobra.Command {
	d := &cobra.Command{
		Use:   "drive",
		Short: "OneDrive operations (app-only, scoped by allowed_mailboxes)",
	}
	d.AddCommand(newDriveLsCmd(), newDriveSearchCmd(), newDriveGetCmd(), newDriveDownloadCmd(), newDriveUploadCmd())
	return d
}

func newDriveLsCmd() *cobra.Command {
	var mailbox, path string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List items in a drive folder (root by default)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Drive().List(cmd.Context(), mbx, path)
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox/user whose drive to list (defaults to default_mailbox)")
	cmd.Flags().StringVar(&path, "path", "", "folder path within the drive (default root)")
	return cmd
}

func newDriveSearchCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search a drive for items",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Drive().Search(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox/user whose drive to search (defaults to default_mailbox)")
	return cmd
}

func newDriveGetCmd() *cobra.Command {
	var mailbox string
	cmd := &cobra.Command{
		Use:   "get <item-id>",
		Short: "Get drive item metadata",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			data, err := client.Drive().Get(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox/user whose drive (defaults to default_mailbox)")
	return cmd
}

func newDriveDownloadCmd() *cobra.Command {
	var mailbox, out string
	cmd := &cobra.Command{
		Use:   "download <item-id> --out <file>",
		Short: "Download a drive item's content to a file",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("--out <file> is required")
			}
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			content, err := client.Drive().Download(cmd.Context(), mbx, args[0])
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, content, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", out, err)
			}
			return output.WriteJSON(os.Stdout, map[string]any{"saved": out, "bytes": len(content)})
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox/user whose drive (defaults to default_mailbox)")
	cmd.Flags().StringVar(&out, "out", "", "local file to write the content to")
	return cmd
}

func newDriveUploadCmd() *cobra.Command {
	var mailbox, dest string
	cmd := &cobra.Command{
		Use:   "upload <local-file>",
		Short: "Upload a local file to a drive (small files, simple upload)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, client, mbx, err := mailContext(mailbox)
			if err != nil {
				return err
			}
			content, err := os.ReadFile(args[0])
			if err != nil {
				return fmt.Errorf("read %s: %w", args[0], err)
			}
			name := dest
			if name == "" {
				name = filepath.Base(args[0])
			}
			data, err := client.Drive().Upload(cmd.Context(), mbx, name, content)
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&mailbox, "mailbox", "", "mailbox/user whose drive (defaults to default_mailbox)")
	cmd.Flags().StringVar(&dest, "dest", "", "destination name/path in the drive (default: local file name)")
	return cmd
}
