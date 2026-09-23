package caldav

import "testing"

func TestPathMatch(t *testing.T) {
	c, ok := calendarPath.Match("/dav/remoon/calendar/default/")
	t.Log(c, ok)
	obj, ok := icsPath.Match("/dav/remoon/calendar/default/default.ics")
	t.Log(obj, ok)
	u, ok := userPath.Match("/dav/remoon/calendar/default/default.ics")
	t.Log(u, ok)
}
