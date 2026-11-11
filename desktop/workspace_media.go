// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/locales"
	"context"
	"encoding/json"
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

// Playback belongs to the workspace; mode and enhancement remain window-wide.
func (a *desktop) mediaForWorkspace(workspace string) *workspaceMediaState {
	key := workspaceKey(workspace)
	if a.mediaPlayback == nil {
		a.mediaPlayback = make(map[string]*workspaceMediaState)
	}
	state := a.mediaPlayback[key]
	if state == nil {
		state = &workspaceMediaState{}
		a.mediaPlayback[key] = state
	}
	a.media.ensureMode()
	state.mode, state.enhance = a.media.mode, a.media.enhance
	return state
}

func (a *desktop) workspaceMedia() *workspaceMediaState {
	if tab := a.current(); tab != nil {
		return a.mediaForWorkspace(tab.Workspace)
	}
	a.media.ensureMode()
	return &a.media
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
	media := a.workspaceMedia()
	if automatic && !media.continueAfterEnd() {
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
			media.autoplay = false
			if p := a.nativeVideoFor(file); p != nil && p.player != nil {
				p.player.Play()
			} else {
				a.mediaCommand(file, "window.allyMedia?.play(true)")
			}
		}
		return
	}
	media.autoplay = automatic || media.playing
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
	localizedSelect(c.Key("media-mode"), &a.media.mode, mediaModes).Width(108).OnChange(func() { a.syncMediaOptions(file) })
	label := locales.Text("播放")
	if a.workspaceMedia().playing {
		label = locales.Text("暂停")
	}
	uikit.Tooltip(c, flat(c, label).OnClick(func() { a.toggleMediaPlayback(file) }), locales.Text("空格：播放/暂停；← →：切换同目录同类型媒体；图片每 5 秒切换"))
	flat(c, locales.Text("上一个")).Disabled(previous == "").OnClick(func() { a.stepWorkspaceMedia(file, -1, false) })
	flat(c, locales.Text("下一个")).Disabled(next == "").OnClick(func() { a.stepWorkspaceMedia(file, 1, false) })
	if workspaceFileKindForPath(file.Path) == workspaceFileImage {
		label := locales.Text("画质增强：关")
		if a.media.enhance {
			label = locales.Text("画质增强：开")
		}
		uikit.Tooltip(c, flat(c, label).OnClick(func() { a.media.enhance = !a.media.enhance; a.syncMediaOptions(file) }), locales.Text("轻微减淡与锐化，仅影响预览"))
	}
}

func (a *desktop) workspaceMediaShortcuts(c *ui.Context, file *workspaceFile) {
	c.OnShortcut(0, ui.KeyLeft, func() { a.stepWorkspaceMedia(file, -1, false) })
	c.OnShortcut(0, ui.KeyRight, func() { a.stepWorkspaceMedia(file, 1, false) })
	c.OnShortcut(0, ui.KeySpace, func() { a.toggleMediaPlayback(file) })
}

func (a *desktop) toggleMediaPlayback(file *workspaceFile) {
	media := a.workspaceMedia()
	media.playing = !media.playing
	if p := a.nativeVideoFor(file); p != nil && p.player != nil {
		if media.playing {
			p.player.Play()
		} else {
			p.player.Pause()
		}
		return
	}
	a.mediaCommand(file, fmt.Sprintf("window.allyMedia?.play(%t)", media.playing))
}

func (a *desktop) syncMediaOptions(file *workspaceFile) {
	if p := a.nativeVideoFor(file); p != nil && p.player != nil {
		p.player.SetLoop(a.media.mode == mediaModeSingle)
		return
	}
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
	key := a.workspaceFilePreviewKey(file)
	surface.offMedia = surface.view.Page().OnPageTitleUpdated(func(e *mygo.TitleEvent) {
		action, ok := workspaceMediaAction(e.Title, token)
		if !ok {
			return
		}
		a.win.Update(func() {
			a.handleWorkspaceMediaAction(surface, file, key, token, action)
		})
	})
}

func (a *desktop) handleWorkspaceMediaAction(surface *nativePreview, file *workspaceFile, key, token, action string) {
	workspace, _, _ := strings.Cut(key, "\x00")
	if surface.IsDestroyed() || surface.mediaToken != token || a.workspaceFiles[workspace] != file {
		return
	}
	media := a.mediaForWorkspace(workspace)
	if action == "play" {
		media.playing = true
		return
	}
	if action == "pause" {
		media.playing = false
		return
	}
	if a.activeFile() != file || a.page != "chat" || !a.explorer || a.mainWindowHidden || a.win != nil && a.win.IsMinimized() {
		if action == "ended" {
			media.playing, media.autoplay = false, false
		}
		return
	}
	switch action {
	case "next":
		a.stepWorkspaceMedia(file, 1, false)
	case "previous":
		a.stepWorkspaceMedia(file, -1, false)
	case "ended":
		a.stepWorkspaceMedia(file, 1, true)
	}
}

// Replace only the decoded image node. No navigation, clearing or native-view
// remount is involved; the old frame remains committed during the bounded load.
func (p *nativePreview) replaceImage(options workspaceMediaOptions) {
	p.retireContent()
	p.state.begin()
	p.state.ready, p.imageDocument = true, true
	p.watchFailures("about:blank")
	generation, browser, update := p.state.generation, p.browser, p.update
	encoded, _ := json.Marshal(options)
	code := fmt.Sprintf("window.allyMedia.replaceImage(%s,%d)", encoded, generation)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	p.probeCancel = cancel
	go func() {
		value, err := browser.EvalContext(ctx, code)
		canceled := ctx.Err() == context.Canceled
		cancel()
		if canceled {
			return
		}
		update(func() {
			if p.IsDestroyed() || p.state.generation != generation || p.state.err != nil {
				return
			}
			p.probeCancel = nil
			if err != nil || value != true {
				if err == nil {
					err = fmt.Errorf("%s", locales.Text("图片替换未完成"))
				}
				p.failContent(generation, fmt.Errorf(locales.Text("图片预览加载失败：%w"), err), false)
			}
		})
	}()
}

// A fixed, dependency-free bridge. The page has no filesystem or host bindings.
const workspaceMediaScript = `<script>
(()=>{
let m=document.querySelector('video,audio,img'); const image=m.tagName==='IMG';
let token=TOKEN;
let playing=false,timer=0,serial=0,revision=0,pending=null,single=false,enhance=false;
const emit=a=>{document.title='ally-media:'+token+':'+a+':'+(++serial)};
const schedule=()=>{clearTimeout(timer);if(image&&playing&&!pending)timer=setTimeout(()=>{if(m.dataset.single)schedule();else{playing=false;emit('ended')}},5000)};
const play=on=>{if(image){playing=on;schedule();emit(on?'play':'pause')}else if(on){m.play().catch(()=>emit('pause'))}else m.pause()};
const configure=(s,e)=>{single=s;enhance=e;m.loop=s;m.dataset.single=s?'1':'';m.style.filter=e?'saturate(.94) brightness(1.025) url(#sharpen)':'';schedule()};
const replaceImage=async(o,id)=>{
 if(!image)throw new Error(ERR_NOT_IMAGE);
 if(id<=revision)return false;
 revision=id;if(pending)pending.cancel();clearTimeout(timer);
 token=o.token;window.allyMedia.token=token;playing=o.start;single=o.single;enhance=o.enhance;
 const next=new Image();next.alt='';
 const job={cancel:null};pending=job;
 try{
  const loaded=await new Promise((resolve,reject)=>{
   let done=false;const finish=(error,ready)=>{if(done)return;done=true;clearTimeout(timeout);next.onload=next.onerror=null;if(!ready)next.removeAttribute('src');if(error)reject(error);else resolve(ready)};
   const timeout=setTimeout(()=>finish(new Error(ERR_IMAGE_TIMEOUT),false),10000);
   job.cancel=()=>finish(null,false);
   next.onerror=()=>finish(new Error(ERR_IMAGE_DECODE),false);
   next.onload=async()=>{try{await next.decode();finish(null,true)}catch(error){finish(error,false)}};
   next.src=o.source;
  });
  if(!loaded||revision!==id)return false;
  configure(single,enhance);next.style.filter=m.style.filter;next.dataset.single=m.dataset.single;
  m.replaceWith(next);m=next;pending=null;play(playing);return true;
 }catch(error){if(pending===job){pending=null;playing=false;emit('pause')}throw error}
};
window.allyMedia={token,play,configure,replaceImage};
if(!image){m.addEventListener('ended',()=>emit('ended'));m.addEventListener('play',()=>emit('play'));m.addEventListener('pause',()=>{if(!m.ended)emit('pause')})}
document.addEventListener('keydown',e=>{if(e.altKey||e.ctrlKey||e.metaKey||e.repeat)return;if(e.key==='ArrowLeft'||e.key==='ArrowRight'){e.preventDefault();emit(e.key==='ArrowLeft'?'previous':'next')}else if(e.code==='Space'){e.preventDefault();play(image?!playing:m.paused)}},true);
m.addEventListener('error',()=>{clearTimeout(timer);emit('pause')});
window.allyMedia.configure(SINGLE,ENHANCE);
if(START) {if(image){if(m.complete)play(true);else m.addEventListener('load',()=>play(true),{once:true})}else play(true)}
})();</script>`
