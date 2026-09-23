package caldav

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/emersion/go-ical"
	"github.com/emersion/go-webdav"
	"github.com/emersion/go-webdav/caldav"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/shynome/err0"
	"github.com/shynome/err0/try"
	"golang.org/x/sync/errgroup"
	"remoon.net/lifemail/db"
)

type Backend struct {
	core.App
}

var _ caldav.Backend = (*Backend)(nil)

func (app *Backend) CurrentUserPrincipal(ctx context.Context) (_ string, err error) {
	r := getAuth(ctx)
	if r == nil {
		return "", ErrUnauthed
	}
	return fmt.Sprintf("/dav/%s/", r.Id), nil
}

func (app *Backend) CalendarHomeSetPath(ctx context.Context) (string, error) {
	r := getAuth(ctx)
	if r == nil {
		return "", ErrUnauthed
	}
	return fmt.Sprintf("/dav/%s/calendar/", r.Id), nil
}

func (app *Backend) CreateCalendar(ctx context.Context, calendar *caldav.Calendar) (err error) {
	_, err = app.createCalendar(ctx, calendar)
	return err
}

func (app *Backend) createCalendar(ctx context.Context, calendar *caldav.Calendar) (_ *core.Record, err error) {
	logger := app.Logger()
	defer err0.Then(&err, nil, func() {
		logger.Error("GetCalendar failed", "error", err)
	})
	acc := getAuthTry(ctx, "")
	logger = logger.With("account", acc.Id)
	cc := try.To1(app.FindCachedCollectionByNameOrId(db.TableCalendars))
	r := core.NewRecord(cc)
	r.Load(map[string]any{
		"account": acc.Id,
		"name":    calendar.Name,
		"desc":    calendar.Description,
	})
	try.To(app.Save(r))
	return r, nil
}

func (app *Backend) ListCalendars(ctx context.Context) (cc []caldav.Calendar, err error) {
	logger := app.Logger()
	defer err0.Then(&err, nil, func() {
		logger.Error("ListCalendars failed", "error", err)
	})
	acc := getAuthTry(ctx, "")
	logger = logger.With("account", acc.Id)
	q := dbx.HashExp{"account": acc.Id}
	rr := try.To1(app.FindAllRecords(db.TableCalendars, q))
	if len(rr) == 0 {
		c := &caldav.Calendar{
			Path: "default",
			Name: "My Calendar",
		}
		r := try.To1(app.createCalendar(ctx, c))
		rr = append(rr, r)
	}
	objects := try.To1(app.FindCollectionByNameOrId(db.TableCalendarObjects))
	icsF := objects.Fields.GetByName("ics").(*core.FileField)
	for _, r := range rr {
		c := NewCalendar(r).ToCalendar(icsF.MaxSize)
		cc = append(cc, c)
	}
	return cc, nil
}
func (app *Backend) GetCalendar(ctx context.Context, path string) (_ *caldav.Calendar, err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("GetCalendar failed", "error", err)
	})
	m, ok := calendarPath.Match(path)
	if !ok {
		return nil, webdav.NewHTTPError(http.StatusBadRequest, ErrPathMatchFailed)
	}
	id := m.Params["calendar"]
	r := try.To1(app.FindRecordById(db.TableCalendars, id))
	objects := try.To1(app.FindCollectionByNameOrId(db.TableCalendarObjects))
	icsF := objects.Fields.GetByName("ics").(*core.FileField)
	c := NewCalendar(r).ToCalendar(icsF.MaxSize)
	return &c, nil
}

func (app *Backend) getCalendarObject(ctx context.Context, path string) (*Object, error) {
	auth := getAuth(ctx)
	m, ok := icsPath.Match(path)
	if !ok {
		err := webdav.NewHTTPError(http.StatusBadRequest, ErrPathMatchFailed)
		return nil, err
	}
	q := `calendar.account = {:acc} && calendar = {:cn} && filename = {:fn}`
	p := dbx.Params{
		"acc": auth.Id,
		"cn":  m.Params["calendar"],
		"fn":  m.Params["ics"],
	}
	r, err := app.FindFirstRecordByFilter(db.TableCalendarObjects, q, p)
	if err != nil {
		return nil, err
	}
	obj := NewObject(r)
	return obj, nil
}
func (app *Backend) GetCalendarObject(ctx context.Context, path string, req *caldav.CalendarCompRequest) (_ *caldav.CalendarObject, err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("GetCalendarObject failed", "error", err)
	})
	auth := getAuthTry(ctx, path)
	obj := try.To1(app.getCalendarObject(ctx, path))
	o := obj.ToObject(auth.Id)
	o.Data = try.To1(obj.ReadData(app))
	return &o, nil
}
func (app *Backend) ListCalendarObjects(ctx context.Context, path string, req *caldav.CalendarCompRequest) (oo []caldav.CalendarObject, err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("ListCalendarObjects failed", "error", err)
	})
	auth := getAuthTry(ctx, path)
	m, ok := calendarPath.Match(path)
	if !ok {
		return nil, webdav.NewHTTPError(http.StatusBadRequest, ErrPathMatchFailed)
	}
	q := `calendar.account = {:acc} && calendar = {:cn}`
	p := dbx.Params{
		"acc": auth.Id,
		"cn":  m.Params["calendar"],
	}
	rr := try.To1(app.FindRecordsByFilter(db.TableCalendarObjects, q, ``, 0, 0, p))
	oo = make([]caldav.CalendarObject, len(rr))
	eg, ctx := errgroup.WithContext(ctx)
	for i, r := range rr {
		eg.Go(func() (err error) {
			obj := NewObject(r)
			o := obj.ToObject(auth.Id)
			o.Data, err = obj.ReadData(app)
			oo[i] = o
			return err
		})
	}
	try.To(eg.Wait())
	return oo, nil
}
func (app *Backend) QueryCalendarObjects(ctx context.Context, path string, query *caldav.CalendarQuery) (_ []caldav.CalendarObject, err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("QueryCalendarObjects failed", "error", err)
	})
	objs, err := app.ListCalendarObjects(ctx, path, &query.CompRequest)
	if err != nil {
		return nil, err
	}
	objs, err = caldav.Filter(query, objs)
	return objs, err
}
func (app *Backend) PutCalendarObject(ctx context.Context, path string, calendar *ical.Calendar, opts *caldav.PutCalendarObjectOptions) (_ *caldav.CalendarObject, err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("PutCalendarObject failed", "error", err)
	})
	auth := getAuthTry(ctx, path)
	m, ok := icsPath.Match(path)
	if !ok {
		return nil, webdav.NewHTTPError(http.StatusBadRequest, ErrPathMatchFailed)
	}
	cid := m.Params["calendar"]
	_, uid, err := caldav.ValidateCalendarObject(calendar)
	if err != nil {
		return nil, caldav.NewPreconditionError(caldav.PreconditionValidCalendarObjectResource)
	}
	fn := uid + ".ics"

	var obj *Object

	// TODO handle IfNoneMatch == ETag
	if opts.IfNoneMatch.IsWildcard() {
		// Make sure we're not overwriting an existing file
		obj, err := app.getCalendarObject(ctx, path)
		if obj != nil {
			return nil, caldav.NewPreconditionError(caldav.PreconditionNoUIDConflict)
		}
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// expect, do nothing
			} else {
				return nil, err
			}
		}
	} else if opts.IfMatch.IsWildcard() {
		// Make sure we _are_ overwriting an existing file
		obj = try.To1(app.getCalendarObject(ctx, path))
	} else if opts.IfMatch.IsSet() {
		// Make sure we overwrite the _right_ file
		obj = try.To1(app.getCalendarObject(ctx, path))
		etag := obj.ToObject(auth.Id).ETag
		want := try.To1(opts.IfMatch.ETag())
		if want != etag {
			err := fmt.Errorf("If-Match does not match current ETag (%s/%s)", want, etag)
			return nil, webdav.NewHTTPError(http.StatusPreconditionFailed, err)
		}
	}

	out := &bytes.Buffer{}
	enc := ical.NewEncoder(out)
	try.To(enc.Encode(calendar))
	f := try.To1(filesystem.NewFileFromBytes(out.Bytes(), fn))

	objects := try.To1(app.FindCachedCollectionByNameOrId(db.TableCalendarObjects))
	if obj == nil {
		obj = NewObject(core.NewRecord(objects))
		obj.Load(map[string]any{
			"calendar": cid,
			"filename": fn,
			"ics":      f,
		})
	} else {
		obj.Set("ics", f)
	}
	try.To(app.Save(obj))

	o := obj.ToObject(auth.Id)
	o.Data = try.To1(obj.ReadData(app))

	return &o, nil
}
func (app *Backend) DeleteCalendarObject(ctx context.Context, path string) (err error) {
	logger := app.Logger().With("path", path)
	defer err0.Then(&err, nil, func() {
		logger.Error("DeleteCalendarObject failed", "error", err)
	})
	auth := getAuthTry(ctx, path)
	m, ok := icsPath.Match(path)
	if !ok {
		return webdav.NewHTTPError(http.StatusBadRequest, ErrPathMatchFailed)
	}
	q := `calendar.account = {:acc} && calendar = {:cn} && filename = {:fn}`
	p := dbx.Params{
		"acc": auth.Id,
		"cn":  m.Params["calendar"],
		"fn":  m.Params["ics"],
	}
	r := try.To1(app.FindFirstRecordByFilter(db.TableCalendarObjects, q, p))
	err = app.Delete(r)
	return err
}

type Calendar struct {
	core.BaseRecordProxy
}

func NewCalendar(r *core.Record) *Calendar {
	c := &Calendar{}
	c.SetProxyRecord(r)
	return c
}

func (c *Calendar) ToCalendar(maxSize int64) caldav.Calendar {
	p := fmt.Sprintf("/dav/%s/calendar/%s/", c.GetString("account"), c.Id)
	return caldav.Calendar{
		Path:                  p,
		Name:                  c.GetString("name"),
		Description:           c.GetString("description"),
		MaxResourceSize:       maxSize,
		SupportedComponentSet: []string{"VEVENT"},
	}
}

type Object struct {
	core.BaseRecordProxy
}

func NewObject(r *core.Record) *Object {
	obj := &Object{}
	obj.SetProxyRecord(r)
	return obj
}

func (obj *Object) ToObject(acc string) caldav.CalendarObject {
	p := fmt.Sprintf("/dav/%s/calendar/%s/%s", acc, obj.GetString("calendar"), obj.GetString("filename"))
	mt := obj.GetDateTime("mod_time").Time()
	o := caldav.CalendarObject{
		Path:          p,
		ModTime:       mt,
		ContentLength: obj.GetInt64("size"),
		ETag:          obj.GetString("etag"),
	}
	return o
}

func (obj *Object) ReadData(app core.App) (_ *ical.Calendar, err error) {
	defer err0.Then(&err, nil, nil)
	fs := try.To1(app.NewFilesystem())
	defer fs.Close()
	key := obj.BaseFilesPath() + "/" + obj.GetString("ics")
	r := try.To1(fs.GetReader(key))
	defer r.Close()
	c := try.To1(ical.NewDecoder(r).Decode())
	return c, nil
}
