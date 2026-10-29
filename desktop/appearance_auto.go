// SPDX-License-Identifier: GPL-3.0-only
package desktop

import "time"

type autoAppearanceState struct {
	Mode       string
	Timer      *time.Timer
	Generation uint64
}

// now is the computer's local time (time.Now in production).
func (a *desktop) refreshAutoAppearance(now time.Time) bool {
	if a.prefs.Appearance != "auto" {
		return false
	}
	previous := a.autoAppearance.Mode
	a.autoAppearance.Mode = "dark"
	if hour := now.Hour(); hour >= 7 && hour < 18 {
		a.autoAppearance.Mode = "light"
	}
	return previous != a.autoAppearance.Mode
}

func (a *desktop) stopAutoAppearance() {
	s := &a.autoAppearance
	if s.Timer != nil {
		s.Timer.Stop()
		s.Timer = nil
	}
	s.Generation++
}

func (a *desktop) selectAppearance(mode string) {
	a.stopAutoAppearance()
	a.prefs.Appearance = mode
	if mode == "auto" {
		a.startAutoAppearance()
	} else if a.win != nil {
		a.syncNativeAppearance()
	}
	a.savePrefs()
}

func (a *desktop) startAutoAppearance() {
	if a.prefs.Appearance != "auto" {
		return
	}
	a.refreshAutoAppearance(time.Now())
	if a.win == nil || a.closing {
		return
	}
	a.syncNativeAppearance()
	a.scheduleAutoAppearance()
}

func (a *desktop) scheduleAutoAppearance() {
	s := &a.autoAppearance
	if s.Timer != nil {
		s.Timer.Stop()
	}
	generation := s.Generation
	now := time.Now()
	// Align to the next minute; recheck after clock changes and sleep/resume.
	delay := time.Minute - time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond())
	s.Timer = time.AfterFunc(delay, func() {
		a.win.Update(func() {
			if a.closing || generation != a.autoAppearance.Generation || a.prefs.Appearance != "auto" {
				return
			}
			if a.refreshAutoAppearance(time.Now()) {
				a.syncNativeAppearance()
			}
			a.scheduleAutoAppearance()
		})
	})
}
