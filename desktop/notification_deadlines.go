// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"github.com/egoist/mygo/ui"
	"time"
)

const allyNoticeDuration = 2 * time.Second

// MyGo pauses toast age while unfocused/hovered. Application notices instead
// expire against wall time, including notices queued while the view is hidden.
func (a *desktop) flushNotices(c *ui.Context, now time.Time) {
	if a.noticeDeadlines == nil {
		a.noticeDeadlines = map[string]time.Time{}
	}
	for key, deadline := range a.noticeRecent {
		if !now.Before(deadline) {
			delete(a.noticeRecent, key)
		}
	}
	pending := a.notifications
	a.notifications = nil
	for _, item := range pending {
		deadline, ok := item.Data.(time.Time)
		if !ok {
			deadline = now.Add(allyNoticeDuration)
		}
		if !now.Before(deadline) {
			continue
		}
		id := c.AddToast(item)
		a.noticeDeadlines[id] = deadline
	}
	for id, deadline := range a.noticeDeadlines {
		if !now.Before(deadline) {
			c.CloseToast(id)
			delete(a.noticeDeadlines, id)
		} else {
			c.After(deadline.Sub(now))
		}
	}
}
