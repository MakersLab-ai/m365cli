package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/output"
)

// newSpCmd is the SharePoint domain root. Sites are scoped by allowed_sites
// (granted per-site via Sites.Selected); see docs/azure-setup.md.
func newSpCmd() *cobra.Command {
	sp := &cobra.Command{
		Use:   "sp",
		Short: "SharePoint operations (app-only, scoped by allowed_sites / Sites.Selected)",
	}
	sp.AddCommand(newSpSitesCmd(), newSpListCmd(), newSpItemsCmd(), newSpDownloadCmd())
	return sp
}

// siteClient builds the backend without resolving a mailbox (SharePoint is
// site-scoped, not mailbox-scoped).
func siteClient() (backend.Backend, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return newBackend(cfg)
}

// newSpSitesCmd is discovery: it finds site IDs to add to allowed_sites. It is
// intentionally not gated by allowed_sites (see graph.SearchSites).
func newSpSitesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sites <query>",
		Short: "Search for sites to discover their IDs (not gated by allowed_sites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := siteClient()
			if err != nil {
				return err
			}
			data, err := client.Sites().Search(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	return cmd
}

func newSpListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <site-id>",
		Short: "List document libraries (drives) in a site",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := siteClient()
			if err != nil {
				return err
			}
			data, err := client.Sites().ListDrives(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	return cmd
}

func newSpItemsCmd() *cobra.Command {
	var path string
	cmd := &cobra.Command{
		Use:   "items <site-id>",
		Short: "List items in a site's default document library",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := siteClient()
			if err != nil {
				return err
			}
			data, err := client.Sites().Items(cmd.Context(), args[0], path)
			if err != nil {
				return err
			}
			return emitData(data)
		},
	}
	cmd.Flags().StringVar(&path, "path", "", "folder path within the library (default root)")
	return cmd
}

func newSpDownloadCmd() *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "download <site-id> <item-id> --out <file>",
		Short: "Download a site drive item's content to a file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return fmt.Errorf("--out <file> is required")
			}
			client, err := siteClient()
			if err != nil {
				return err
			}
			content, err := client.Sites().Download(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, content, 0o600); err != nil {
				return fmt.Errorf("write %s: %w", out, err)
			}
			return output.WriteJSON(os.Stdout, map[string]any{"saved": out, "bytes": len(content)})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "local file to write the content to")
	return cmd
}
