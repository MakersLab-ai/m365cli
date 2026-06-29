package graphbackend

import (
	"context"
	"net/url"

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
