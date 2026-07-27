package graphbackend

import (
	"context"
	"net/url"
	"strings"

	"github.com/MakersLab-ai/m365cli/internal/graph"
)

type driveSvc struct{ c *graph.Client }

const driveItemSelect = "id,name,size,folder,file,lastModifiedDateTime,webUrl"

func (s driveSvc) List(ctx context.Context, mailbox, path string) ([]byte, error) {
	var suffix string
	if path == "" || path == "/" {
		suffix = "drive/root/children?$select=" + driveItemSelect
	} else {
		suffix = "drive/root:/" + escapeDrivePath(path) + ":/children?$select=" + driveItemSelect
	}
	return unwrapValue(s.c.GetForMailbox(ctx, mailbox, suffix))
}

func (s driveSvc) Search(ctx context.Context, mailbox, query string) ([]byte, error) {
	suffix := "drive/root/search(q='" + url.QueryEscape(query) + "')?$select=" + driveItemSelect
	return unwrapValue(s.c.GetForMailbox(ctx, mailbox, suffix))
}

func (s driveSvc) Get(ctx context.Context, mailbox, id string) ([]byte, error) {
	return s.c.GetForMailbox(ctx, mailbox, "drive/items/"+url.PathEscape(id)+"?$select="+driveItemSelect)
}

func (s driveSvc) Download(ctx context.Context, mailbox, id string) ([]byte, error) {
	return s.c.GetForMailbox(ctx, mailbox, "drive/items/"+url.PathEscape(id)+"/content")
}

func (s driveSvc) Upload(ctx context.Context, mailbox, destName string, content []byte) ([]byte, error) {
	suffix := "drive/root:/" + escapeDrivePath(destName) + ":/content"
	return s.c.PutRawForMailbox(ctx, mailbox, suffix, "application/octet-stream", content)
}

// escapeDrivePath escapes each path segment while keeping the slashes that
// separate drive folders. Shared by drive and site item listings.
func escapeDrivePath(p string) string {
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}
