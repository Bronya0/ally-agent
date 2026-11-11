// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"image"
	"io"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type videoTestDecoder struct{ next int }

func (d *videoTestDecoder) Next() (ui.VideoFrame, error) {
	if d.next > 0 {
		return ui.VideoFrame{}, io.EOF
	}
	d.next++
	return ui.VideoFrame{Image: image.NewRGBA(image.Rect(0, 0, 2, 2)), Duration: time.Second}, nil
}
func (d *videoTestDecoder) Rewind() error { d.next = 0; return nil }

func TestNativeVideoResetClosesPlayerAndInvalidatesCallbacks(t *testing.T) {
	player, err := ui.NewVideoPlayer(&videoTestDecoder{})
	if err != nil {
		t.Fatal(err)
	}
	p := &nativeVideoPreview{player: player, timer: time.NewTimer(time.Hour)}
	p.Reset()
	if p.player != nil || p.timer != nil || p.generation != 1 || p.IsDestroyed() {
		t.Fatal("reset retained the player or failed to invalidate callbacks")
	}
	p.Destroy()
	if !p.IsDestroyed() || p.generation != 2 {
		t.Fatal("destroy did not retire the preview")
	}
}

func TestNativeVideoKeepsWorkspaceWebViewAndReleasesBoth(t *testing.T) {
	root := t.TempDir()
	web := &fakeWorkspacePreview{body: "image"}
	life := workspacePreviewLifecycle{mounted: map[string]*mountedWorkspacePreview{workspaceKey(root): {key: workspacePreviewTestKey(root, "image.jpg"), surface: web, autoplay: true}}}
	_, pending := life.beginLoad(root, "clip.mp4")
	life.finishLoad(pending)
	video := &nativeVideoPreview{}
	key := workspacePreviewTestKey(root, "clip.mp4")
	if err := life.mount(key, true, func() (workspacePreviewSurface, error) { return video, nil }, func(workspacePreviewSurface) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if life.mountedFor(key) != video || web.destroys != 0 {
		t.Fatal("video replaced the retained workspace WebView")
	}
	_, pending = life.beginLoad(root, "next.jpg")
	life.finishLoad(pending)
	if life.mountedFor(key) != nil || video.generation != 1 || web.destroys != 0 {
		t.Fatal("switch did not stop video while retaining WebView")
	}
	life.releaseWorkspace(root)
	if !video.IsDestroyed() || web.destroys != 1 {
		t.Fatal("workspace close failed to release both preview resources")
	}
}

func TestVideoTime(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{-time.Second, "0:00"}, {65 * time.Second, "1:05"}, {time.Hour + 2*time.Minute + 3*time.Second, "1:02:03"}} {
		if got := videoTime(tc.d); got != tc.want {
			t.Fatalf("videoTime(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
