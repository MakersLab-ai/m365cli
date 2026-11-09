package graphbackend

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/graph"
)

type siteSvc struct{ c *graph.Client }

func (s siteSvc) Search(ctx context.Context, query string) ([]byte, error) {
	return unwrapValue(s.c.SearchSites(ctx, query))
}

func (s siteSvc) ListDrives(ctx context.Context, siteID string) ([]byte, error) {
	return unwrapValue(s.c.GetForSite(ctx, siteID, "drives?$select=id,name,webUrl,driveType"))
}

func (s siteSvc) Items(ctx context.Context, siteID, path string) ([]byte, error) {
	var suffix string
	if path == "" || path == "/" {
		suffix = "drive/root/children?$select=" + driveItemSelect
	} else {
		suffix = "drive/root:/" + escapeDrivePath(path) + ":/children?$select=" + driveItemSelect
	}
	return unwrapValue(s.c.GetForSite(ctx, siteID, suffix))
}

func (s siteSvc) Download(ctx context.Context, siteID, itemID string) ([]byte, error) {
	return s.c.GetForSite(ctx, siteID, "drive/items/"+url.PathEscape(itemID)+"/content")
}

// Upload is a simple (single-request) upload into the site's default library.
// Graph replaces an existing file by default; we ask it to fail instead unless
// the caller explicitly wants to overwrite.
func (s siteSvc) Upload(ctx context.Context, siteID, folder, name string, content []byte, overwrite bool) ([]byte, error) {
	name = strings.Trim(name, "/")
	if name == "" {
		return nil, fmt.Errorf("upload needs a file name")
	}
	dest := name
	if f := strings.Trim(folder, "/"); f != "" {
		dest = f + "/" + name
	}
	behavior := "fail"
	if overwrite {
		behavior = "replace"
	}
	suffix := "drive/root:/" + escapeDrivePath(dest) + ":/content?@microsoft.graph.conflictBehavior=" + behavior
	return s.c.PutRawForSite(ctx, siteID, suffix, "application/octet-stream", content)
}

// Move resolves destFolder (must exist and be a folder), then PATCHes the
// source item's parentReference. Both requests pass the allowed_sites choke
// point; conflictBehavior=fail keeps an existing file in the target untouched.
func (s siteSvc) Move(ctx context.Context, siteID, srcPath, destFolder string) ([]byte, error) {
	src, dst := strings.Trim(srcPath, "/"), strings.Trim(destFolder, "/")
	if src == "" {
		return nil, fmt.Errorf("move needs the source item path (--path)")
	}
	if dst == "" {
		return nil, fmt.Errorf("move needs the target folder path (--to)")
	}
	raw, err := s.c.GetForSite(ctx, siteID, "drive/root:/"+escapeDrivePath(dst)+"?$select=id,name,folder")
	if err != nil {
		return nil, fmt.Errorf("resolve target folder %q: %w", dst, err)
	}
	var target struct {
		ID     string          `json:"id"`
		Folder json.RawMessage `json:"folder"`
	}
	if err := json.Unmarshal(raw, &target); err != nil {
		return nil, fmt.Errorf("resolve target folder %q: %w", dst, err)
	}
	if target.ID == "" || len(target.Folder) == 0 || string(target.Folder) == "null" {
		return nil, fmt.Errorf("target %q is not a folder", dst)
	}
	payload, err := json.Marshal(map[string]any{"parentReference": map[string]string{"id": target.ID}})
	if err != nil {
		return nil, err
	}
	suffix := "drive/root:/" + escapeDrivePath(src) + "?@microsoft.graph.conflictBehavior=fail"
	return s.c.PatchForSite(ctx, siteID, suffix, payload)
}
