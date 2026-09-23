package caldav

import (
	"errors"

	"github.com/ucarion/urlpath"
)

var icsPath = urlpath.New("/dav/:account/calendar/:calendar/:ics")
var calendarPath = urlpath.New("/dav/:account/calendar/:calendar/")
var userPath = urlpath.New("/dav/:account/*")

var ErrPathMatchFailed = errors.New("path params match failed")
