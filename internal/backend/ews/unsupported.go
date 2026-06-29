package ewsbackend

import (
	"context"

	"github.com/MakersLab-ai/m365cli/internal/backend"
	"github.com/MakersLab-ai/m365cli/internal/calendar"
	"github.com/MakersLab-ai/m365cli/internal/contacts"
)

// The calendar, contacts, drive and site domains are not yet implemented for
// EWS. Each method returns backend.ErrUnsupported so the CLI fails with a clear
// message rather than a nil-pointer panic.

type calSvc struct{}

func (calSvc) List(context.Context, string, backend.CalListOpts) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (calSvc) Get(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (calSvc) Create(context.Context, string, calendar.Event) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (calSvc) Update(context.Context, string, string, calendar.Event) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (calSvc) Delete(context.Context, string, string) error { return backend.ErrUnsupported }
func (calSvc) FreeBusy(context.Context, string, backend.ScheduleQuery) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (calSvc) FindTimes(context.Context, string, backend.FindTimesQuery) ([]byte, error) {
	return nil, backend.ErrUnsupported
}

type contactSvc struct{}

func (contactSvc) List(context.Context, string, backend.ListOpts) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (contactSvc) Get(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (contactSvc) Add(context.Context, string, contacts.Contact) ([]byte, error) {
	return nil, backend.ErrUnsupported
}

type driveSvc struct{}

func (driveSvc) List(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (driveSvc) Search(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (driveSvc) Get(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (driveSvc) Download(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (driveSvc) Upload(context.Context, string, string, []byte) ([]byte, error) {
	return nil, backend.ErrUnsupported
}

type siteSvc struct{}

func (siteSvc) Search(context.Context, string) ([]byte, error) { return nil, backend.ErrUnsupported }
func (siteSvc) ListDrives(context.Context, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (siteSvc) Items(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
func (siteSvc) Download(context.Context, string, string) ([]byte, error) {
	return nil, backend.ErrUnsupported
}
