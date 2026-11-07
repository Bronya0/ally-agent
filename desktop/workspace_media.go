// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"ally-dev/desktop/uikit"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Playlist policies, shared by every media preview in this window.
const (
	mediaModeNone       = "不循环"
	mediaModeSequential = "顺序循环"
	mediaModeRandom     = "随机循环"
	mediaModeSingle     = "单一循环"
)

// mediaTitlePrefix marks page-to-host signals; it must match the prefix used
// by workspaceMediaScript.
const mediaTitlePrefix = "ally-media:"

var mediaModes = []string{mediaModeNone, mediaModeSequential, mediaModeRandom, mediaModeSingle}

// Playback options live only in this desktop instance, never in persisted prefs.
type workspaceMediaState struct {
	mode     string
	playing  bool
	enhance  bool
	autoplay bool
}

func (s *workspaceMediaState) ensureMode() {
	if s.mode == "" {
		s.mode = mediaModeNone
	}
}

// continueAfterEnd preserves playback intent across a playlist transition.
// Single-repeat stays in the current element; no-repeat stops at the end.
func (s *workspaceMediaState) continueAfterEnd() bool {
	s.ensureMode()
	if s.mode == mediaModeNone {
		s.playing, s.autoplay = false, false
		return false
	}
	if !s.playing || s.mode == mediaModeSingle {
		return false
	}
	s.autoplay = true
	return true
}

func mediaNextIndex(index, count int, mode string, delta int) int {
	if count == 0 || index < 0 {
		return -1
	}
	if mode == mediaModeRandom && count > 1 {
		return (index + 1 + rand.IntN(count-1)) % count
	}
	return (index + delta + count) % count
}

// mediaPlaylist lists the files beside file that share its kind, plus the
// index of file itself (-1 when the folder snapshot does not contain it).
func (a *desktop) mediaPlaylist(file *workspaceFile) ([]string, int) {
	parent := fileParent(file.Path)
	entries := a.files
	if parent != "" && parent != "." {
		entries = a.fileChildren[parent]
	}
	kind := workspaceFileKindForPath(file.Path)
	current := normalizeExplorerPath(file.Path)
	paths := make([]string, 0, len(entries))
	index := -1
	for _, entry := range entries {
		if entry.Dir || workspaceFileKindForPath(entry.Path) != kind {
			continue
		}
		if normalizeExplorerPath(entry.Path) == current {
			index = len(paths)
		}
		paths = append(paths, entry.Path)
	}
	return paths, index
}

// mediaNeighbours resolves where a manual step would land. An empty path means
// the step would stay on the current file, so its button stays disabled.
func (a *desktop) mediaNeighbours(file *workspaceFile) (previous, next string) {
	paths, index := a.mediaPlaylist(file)
	pick := func(delta int) string {
		target := mediaNextIndex(index, len(paths), a.media.mode, delta)
		if target < 0 || target == index {
			return ""
		}
		return paths[target]
	}
	return pick(-1), pick(1)
}

func (a *desktop) stepWorkspaceMedia(file *workspaceFile, delta int, automatic bool) {
	if a.activeFile() != file {
		return
	}
	if automatic && !a.media.continueAfterEnd() {
		return
	}
	previous, next := a.mediaNeighbours(file)
	target := next
	if delta < 0 {
		target = previous
	}
	if target == "" {
		if automatic {
			// A one-file playlist still repeats without creating a new preview.
			a.media.autoplay = false
			a.mediaCommand(file, "window.allyMedia?.play(true)")
		}
		return
	}
	a.media.autoplay = automatic || a.media.playing
	a.openFile(target)
}

func (a *desktop) mediaCommand(file *workspaceFile, command string) {
	surface, ok := a.workspacePreview.mountedFor(a.workspaceFilePreviewKey(file)).(*nativePreview)
	if !ok || surface.mediaToken == "" {
		return
	}
	// The async command may reach a reused view after another file loads.
	command = fmt.Sprintf("if(window.allyMedia?.token===%q){%s}", surface.mediaToken, command)
	page := surface.view.Page()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = page.EvalContext(ctx, command)
	}()
}

func (a *desktop) workspaceMediaToolbar(c *ui.Context, file *workspaceFile) {
	previous, next := a.mediaNeighbours(file)
	uikit.Select(c.Key("media-mode"), &a.media.mode, mediaModes).Width(108).OnChange(func() { a.syncMediaOptions(file) })
	label := "播放"
	if a.media.playing {
		label = "暂停"
	}
	uikit.Tooltip(c, flat(c, label).OnClick(func() { a.toggleMediaPlayback(file) }), "空格：播放/暂停；← →：切换同目录同类型媒体；图片每 5 秒切换")
	flat(c, "上一个").Disabled(previous == "").OnClick(func() { a.stepWorkspaceMedia(file, -1, false) })
	flat(c, "下一个").Disabled(next == "").OnClick(func() { a.stepWorkspaceMedia(file, 1, false) })
	if workspaceFileKindForPath(file.Path) == workspaceFileImage {
		label := "画质增强：关"
		if a.media.enhance {
			label = "画质增强：开"
		}
		uikit.Tooltip(c, flat(c, label).OnClick(func() { a.media.enhance = !a.media.enhance; a.syncMediaOptions(file) }), "轻微减淡与锐化，仅影响预览")
	}
}

func (a *desktop) workspaceMediaShortcuts(c *ui.Context, file *workspaceFile) {
	c.OnShortcut(0, ui.KeyLeft, func() { a.stepWorkspaceMedia(file, -1, false) })
	c.OnShortcut(0, ui.KeyRight, func() { a.stepWorkspaceMedia(file, 1, false) })
	c.OnShortcut(0, ui.KeySpace, func() { a.toggleMediaPlayback(file) })
}

func (a *desktop) toggleMediaPlayback(file *workspaceFile) {
	a.media.playing = !a.media.playing
	a.mediaCommand(file, fmt.Sprintf("window.allyMedia?.play(%t)", a.media.playing))
}

func (a *desktop) syncMediaOptions(file *workspaceFile) {
	a.mediaCommand(file, fmt.Sprintf("window.allyMedia?.configure(%t,%t)", a.media.mode == mediaModeSingle, a.media.enhance))
}

func workspaceMediaAction(title, token string) (string, bool) {
	if token == "" {
		return "", false
	}
	rest, ok := strings.CutPrefix(title, mediaTitlePrefix+token+":")
	if !ok {
		return "", false
	}
	action, serial, ok := strings.Cut(rest, ":")
	if !ok || serial == "" {
		return "", false
	}
	switch action {
	case "next", "previous", "ended", "play", "pause":
		return action, true
	default:
		return "", false
	}
}

func (a *desktop) attachWorkspaceMedia(surface *nativePreview, file *workspaceFile, token string) {
	if surface.offMedia != nil {
		surface.offMedia()
	}
	surface.mediaToken = token
	surface.offMedia = surface.view.Page().OnPageTitleUpdated(func(e *mygo.TitleEvent) {
		action, ok := workspaceMediaAction(e.Title, token)
		if !ok {
			return
		}
		a.win.Update(func() {
			// The surface can report before the lifecycle mounts it, so only the
			// active file decides whether the signal still belongs here.
			if surface.IsDestroyed() || surface.mediaToken != token || a.activeFile() != file {
				return
			}
			switch action {
			case "next":
				a.stepWorkspaceMedia(file, 1, false)
			case "previous":
				a.stepWorkspaceMedia(file, -1, false)
			case "ended":
				a.stepWorkspaceMedia(file, 1, true)
			case "play":
				a.media.playing = true
			case "pause":
				a.media.playing = false
			}
		})
	})
}

// A fixed, dependency-free bridge. The page has no filesystem or host bindings.
const workspaceMediaScript = `<script>
(()=>{
const m=document.querySelector('video,audio,img'); const image=m.tagName==='IMG';
const token=TOKEN;
let playing=false,timer=0,serial=0;
const emit=a=>{document.title='ally-media:'+token+':'+a+':'+(++serial)};
const schedule=()=>{clearTimeout(timer);if(image&&playing)timer=setTimeout(()=>{if(m.dataset.single)schedule();else{playing=false;emit('ended')}},5000)};
const play=on=>{if(image){playing=on;schedule();emit(on?'play':'pause')}else if(on){m.play().catch(()=>emit('pause'))}else m.pause()};
window.allyMedia={token,play,configure:(single,enhance)=>{m.loop=single;m.dataset.single=single?'1':'';m.style.filter=enhance?'saturate(.94) brightness(1.025) url(#sharpen)':'';schedule()}};
if(!image){m.addEventListener('ended',()=>emit('ended'));m.addEventListener('play',()=>emit('play'));m.addEventListener('pause',()=>{if(!m.ended)emit('pause')})}
document.addEventListener('keydown',e=>{if(e.altKey||e.ctrlKey||e.metaKey||e.repeat)return;if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();emit(e.key==='ArrowLeft'?'previous':'next')}else if(e.code==='Space'){e.preventDefault();play(image?!playing:m.paused)}},true);
m.addEventListener('error',()=>{clearTimeout(timer);emit('pause')});
window.allyMedia.configure(SINGLE,ENHANCE);
if(START) {if(image){if(m.complete)play(true);else m.addEventListener('load',()=>play(true),{once:true})}else play(true)}
})();</script>`
