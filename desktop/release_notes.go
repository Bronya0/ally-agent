// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/resources"
	"encoding/json"
	"log"
	"regexp"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

var releaseNotesVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+$`)

type bundledReleaseNotes struct {
	Version string `json:"version"`
	Body    string `json:"body"`
}

func (a *desktop) loadReleaseNotes() {
	data, err := resources.Assets.ReadFile("release_notes.json")
	var notes bundledReleaseNotes
	if err == nil {
		err = json.Unmarshal(data, &notes)
	}
	if err != nil {
		log.Printf("read bundled release notes: %v", err)
		return
	}
	a.upd.ReleaseVer = strings.TrimSpace(notes.Version)
	a.upd.ReleaseBody = strings.TrimSpace(notes.Body)
}

func shouldShowReleaseNotes(version, body, seen string) bool {
	version = strings.TrimSpace(version)
	return releaseNotesVersion.MatchString(version) && strings.TrimSpace(body) != "" && version != strings.TrimSpace(seen)
}

// Called after startup completes, never while building a view.
func (a *desktop) maybeShowReleaseNotes() {
	a.upd.ReleaseOpen = a.ready && shouldShowReleaseNotes(a.upd.ReleaseVer, a.upd.ReleaseBody, a.prefs.SeenReleaseNotes)
}

func (a *desktop) closeReleaseNotes() {
	a.upd.ReleaseOpen = false
	if a.upd.ReleaseVer != "" && a.prefs.SeenReleaseNotes != a.upd.ReleaseVer {
		a.prefs.SeenReleaseNotes = a.upd.ReleaseVer
		a.savePrefs()
	}
}

func (a *desktop) releaseNotesMarkdown(c *ui.Context, options **markdown.Options, scope string) {
	if *options == nil {
		*options = a.readingMarkdownOptions(scope)
	}
	a.configureReadingMarkdown(*options)
	markdown.View(c.Key(scope), a.upd.ReleaseBody, *options).FillWidth().MinWidth(0)
}

func (a *desktop) releaseNotesDialog(c *ui.Context) {
	if !a.upd.ReleaseOpen {
		return
	}
	_, h := c.Size()
	dialog := ui.DialogBase(c.Key("release-notes-dialog"), &a.upd.ReleaseOpen, func(backdrop, panel ui.Element) {
		modalBackdrop(backdrop)
		modalPanelChrome(c, panel, widthModalWide)
		ui.Column(c).FillWidth().Gap(gapSection).Selectable().Children(func() {
			modalTitle(c, "本次更新", "版本 "+a.upd.ReleaseVer+" · 新版本首次启动时显示一次")
			ui.Scroll(c.Key("release-notes-scroll")).FillWidth().MinHeight(0).MaxHeight(min(560, h*0.62)).Label("版本亮点正文").Children(func() {
				a.releaseNotesMarkdown(c, &a.upd.ReleaseDialogMD, "release-notes-dialog-markdown")
			})
			modalActions(c, func() {
				primary(c, "关闭").OnClick(a.closeReleaseNotes)
			})
		})
	})
	if dialog.Dismissed() {
		a.closeReleaseNotes()
	}
}
