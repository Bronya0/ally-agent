// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"strings"
	"time"

	"ally-dev/desktop/uikit"
	"ally-dev/locales"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/video"
)

// A workspace owns its player until the preview is replaced or closed. All
// player operations and timer results run on the UI thread.
type nativeVideoPreview struct {
	player          *ui.VideoPlayer
	timer           *time.Timer
	generation      uint64
	watchGeneration uint64
	ended           bool
	paused          bool
	destroyed       bool
}

func (p *nativeVideoPreview) Native() ui.NativeWebView { return nil }
func (p *nativeVideoPreview) IsDestroyed() bool        { return p.destroyed }
func (p *nativeVideoPreview) LoadHTML(string)          {}
func (p *nativeVideoPreview) LoadFile(path string) error {
	player, err := video.Open(path)
	if err != nil {
		return err
	}
	p.player = player
	p.ended, p.paused = false, false
	return nil
}
func (p *nativeVideoPreview) Reset() {
	p.generation++
	p.watchGeneration++
	p.ended, p.paused = false, true
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.player != nil {
		p.player.Close()
		p.player = nil
	}
}
func (p *nativeVideoPreview) Destroy() { p.Reset(); p.destroyed = true }

func (p *nativeVideoPreview) pause() {
	p.paused = true
	p.watchGeneration++
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	if p.player != nil {
		p.player.Pause()
	}
}

func (a *desktop) nativeVideoFor(file *workspaceFile) *nativeVideoPreview {
	t := a.current()
	if t == nil {
		return nil
	}
	return a.nativeVideoForWorkspace(t.Workspace, file)
}

func (a *desktop) nativeVideoForWorkspace(root string, file *workspaceFile) *nativeVideoPreview {
	p, _ := a.workspacePreview.mountedFor(workspaceFilePreviewKeyFor(root, file)).(*nativeVideoPreview)
	return p
}

// Engine events are consumed by MyGo when Video is built. Polling also updates
// the controls, detects playback completion and surfaces asynchronous errors.
func (a *desktop) watchWorkspaceVideo(p *nativeVideoPreview, file *workspaceFile, key string) {
	if p == nil || p.paused || p.player == nil {
		return
	}
	p.watchGeneration++
	watchGeneration := p.watchGeneration
	generation := p.generation
	var tick func()
	tick = func() {
		if p.paused || p.watchGeneration != watchGeneration || p.player == nil {
			return
		}
		p.timer = time.AfterFunc(250*time.Millisecond, func() {
			a.win.Update(func() {
				if p.watchGeneration != watchGeneration {
					return
				}
				p.timer = nil
				if p.destroyed || p.paused || p.generation != generation || p.player == nil || a.workspacePreview.mountedFor(key) != p {
					return
				}
				player := p.player
				if err := player.Err(); err != nil {
					file.PreviewError = err.Error()
					workspace, _, _ := strings.Cut(key, "\x00")
					state := a.mediaForWorkspace(workspace)
					state.playing, state.autoplay = false, false
					p.Reset()
					return
				}
				if player.Ended() {
					if p.ended {
						p.paused = true
						p.watchGeneration++
						p.timer = nil
						return
					}
					p.ended = true
					if a.workspaceMediaPreviewVisible(file) && a.stepWorkspaceMedia(file, 1, true) {
						if p.generation == generation && p.player == player && !p.ended {
							tick()
						} else {
							p.timer = nil
						}
						return
					}
					workspace, _, _ := strings.Cut(key, "\x00")
					state := a.mediaForWorkspace(workspace)
					state.playing, state.autoplay = false, false
					p.paused = true
					p.watchGeneration++
					p.timer = nil
					return
				}
				tick()
			})
		})
	}
	tick()
}

func videoTime(d time.Duration) string {
	seconds := max(int64(d/time.Second), 0)
	if seconds >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", seconds/3600, seconds/60%60, seconds%60)
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

func (a *desktop) workspaceVideoView(c *ui.Context, file *workspaceFile, p *nativeVideoPreview) {
	player := p.player
	if player == nil {
		return
	}
	ui.Column(c.Key("native-video")).Grow(1).MinHeight(0).FillWidth().Background(ui.RGB(27, 27, 27)).Children(func() {
		ui.Box(c).Grow(1).MinHeight(0).FillWidth().Center().Children(func() {
			ui.Video(c.Key("video-frame"), player).Fill().Fit(ui.Contain)
		})
		ui.Row(c).FillWidth().Padding(8).Gap(8).Children(func() {
			flat(c, locales.Text("后退 10 秒")).OnClick(func() { player.Seek(max(player.Position()-10*time.Second, 0)) })
			flat(c, locales.Text("前进 10 秒")).OnClick(func() { player.Seek(min(player.Position()+10*time.Second, player.Duration())) })
			ui.Text(c, videoTime(player.Position())).FontSize(fsSmall).TextColor(ui.RGB(220, 220, 220)).NoWrap()
			duration := player.Duration()
			position := player.Position().Seconds()
			slider := uikit.Slider(c.Key("video-position"), &position, 0, max(duration.Seconds(), 0.001)).Grow(1).MinWidth(40).Disabled(duration <= 0)
			if slider.Changed() {
				player.Seek(time.Duration(position * float64(time.Second)))
			}
			ui.Text(c, videoTime(duration)).FontSize(fsSmall).TextColor(ui.RGB(220, 220, 220)).NoWrap()
			label := locales.Text("静音")
			if player.Muted() {
				label = locales.Text("取消静音")
			}
			flat(c, label).OnClick(func() { player.SetMuted(!player.Muted()) })
			volume := player.Volume()
			if uikit.Slider(c.Key("video-volume"), &volume, 0, 1).Width(80).Changed() {
				player.SetVolume(volume)
			}
		})
	})
}
