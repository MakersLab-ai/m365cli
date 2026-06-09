package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"

	"github.com/spf13/cobra"

	"github.com/MakersLab-ai/m365cli/internal/graph"
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

// siteClient builds a Graph client without resolving a mailbox (SharePoint is
// site-scoped, not mailbox-scoped).
func siteClient() (*graph.Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	return newGraphClient(cfg)
}

// emitSiteValue runs a site-scoped GET and emits the `value` array.
func emitSiteValue(cmd *cobra.Command, client *graph.Client, siteID, suffix string) error {
	body, err := client.GetForSite(cmd.Context(), siteID, suffix)
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

// newSpSitesCmd is discovery: it finds site IDs to add to allowed_sites. It is
// intentionally not gated by allowed_sites (see graph.SearchSites).
func newSpSitesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sites <query>",
		Short: "Search for sites to discover their IDs (not gated by allowed_sites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			client, err := newGraphClient(cfg)
			if err != nil {
				return err
			}
			body, err := client.SearchSites(cmd.Context(), args[0])
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
			return emitSiteValue(cmd, client, args[0], "drives?$select=id,name,webUrl,driveType")
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
			var suffix string
			if path == "" || path == "/" {
				suffix = "drive/root/children?$select=" + driveItemSelect
			} else {
				suffix = "drive/root:/" + escapeDrivePath(path) + ":/children?$select=" + driveItemSelect
			}
			return emitSiteValue(cmd, client, args[0], suffix)
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
			content, err := client.GetForSite(cmd.Context(), args[0], "drive/items/"+url.PathEscape(args[1])+"/content")
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
