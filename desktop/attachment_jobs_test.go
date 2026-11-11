package desktop

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAttachmentCompletionBalancesEverySuccessExit(t *testing.T) {
	for _, state := range []string{"visible", "hidden", "closing", "stale", "released"} {
		t.Run(state, func(t *testing.T) {
			conv := &conversation{ID: "session", AttachmentImageJobs: 1,
				AttachmentImages: map[string]*ui.Bitmap{}, AttachmentImageVersions: map[string]uint64{"image": 1}}
			a := &desktop{attachmentImageCacheSessions: []string{conv.ID}, attachmentImageJobSessions: map[*conversation]struct{}{conv: {}}}
			switch state {
			case "hidden":
				a.mainWindowHidden = true
			case "closing":
				a.closing = true
			case "stale":
				conv.AttachmentImageVersions["image"] = 2
			case "released":
				conv.AttachmentImages = nil
			}
			bm := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 2, 2)))
			defer bm.ReleasePixels()
			a.completeAttachmentImage(conv, "image", 1, bm)
			if conv.AttachmentImageJobs != 0 || len(a.attachmentImageJobSessions) != 0 || conv.AttachmentImageVersions != nil {
				t.Fatal("completed decode retained a job or session registration")
			}
			if state == "visible" && conv.AttachmentImages["image"] != bm {
				t.Fatal("successful bitmap not cached")
			}
			a.releaseAttachmentImages(conv)
			if conv.AttachmentImages != nil {
				t.Fatal("completed job prevents cache release")
			}
		})
	}
}
