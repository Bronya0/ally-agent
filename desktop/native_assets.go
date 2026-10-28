// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"time"

	"ally-dev/resources"

	"github.com/egoist/mygo/ui"
)

var originalRailIcons = func() map[string]*ui.SVG {
	result := map[string]*ui.SVG{}
	for _, name := range []string{"chat", "kb", "skills", "mcp", "models", "ssh", "tasks", "stats", "plugins", "settings", "history", "github"} {
		data, err := resources.Assets.ReadFile("icons/antd/" + name + ".svg")
		if err == nil {
			if icon, err := ui.ParseSVG(data); err == nil {
				result[name] = icon
			}
		}
	}
	return result
}()

func normalizedAvatarStyle(style string) string {
	if style == "image" {
		return "glm"
	}
	return style
}

// Only the selected character is decoded; the compressed embedded artwork
// remains available for a later selection without retaining every bitmap.
func decodeAvatarStyle(style string) map[string]*ui.Bitmap {
	style = normalizedAvatarStyle(style)
	result := map[string]*ui.Bitmap{}
	if style == "eye" || style == "custom" {
		return result
	}
	for _, pose := range []string{"idle", "proud", "stretch", "happy"} {
		if (style == "whale" || style == "bot") && pose != "idle" {
			continue
		}
		path := fmt.Sprintf("avatar/%s/%s.webp", style, pose)
		if style == "whale" {
			path = "avatar/whale/delighted-320.webp"
		}
		if style == "bot" {
			continue // Animated WebP is decoded into bounded PNG frames below.
		}
		data, err := resources.Assets.ReadFile(path)
		if err != nil {
			continue
		}
		bitmap, err := ui.DecodeBitmap(data)
		if err == nil {
			result[pose] = bitmap
		}
	}
	if style == "bot" {
		for n := 0; n < 100; n++ {
			data, err := resources.Assets.ReadFile(fmt.Sprintf("avatar/bot/frame%02d.png", n))
			if err == nil {
				if bitmap, err := ui.DecodeBitmap(data); err == nil {
					result[fmt.Sprintf("frame%02d", n)] = bitmap
				}
			}
		}
	}
	return result
}

func (a *desktop) selectAvatarStyle(style string) {
	a.prefs.Avatar = style
	a.savePrefs()
	a.avatarLoadID++
	id := a.avatarLoadID
	releaseAvatarPoses(a.avatarPoses)
	a.avatarStyle, a.avatarPoses = normalizedAvatarStyle(style), nil
	if style == "custom" || style == "eye" || a.win == nil {
		return
	}
	go func() {
		poses := decodeAvatarStyle(style)
		a.win.Update(func() {
			if !a.closing && id == a.avatarLoadID {
				a.avatarPoses = poses
			} else {
				releaseAvatarPoses(poses)
			}
		})
	}()
}

var eyeArtwork = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="164" height="164" viewBox="0 0 164 164"><path d="M17 82c14-28 38-43 65-43s51 15 65 43c-14 28-38 43-65 43S31 110 17 82Z" fill="#d9e1fa"/><circle cx="82" cy="82" r="47" fill="#8da5df" stroke="#536a9e" stroke-width="2"/><path d="M82 34 Q71 82 82 130 Q93 82 82 34Z" fill="#252c42"/><path d="M85 50 L78 76 L83 76 L75 114 L91 70 L84 70Z" fill="#e7edff"/><path d="M26 82c13 18 32 28 56 28s43-10 56-28" fill="none" stroke="#8da5df" stroke-width="2"/></svg>`))

func (a *desktop) avatarImage(c *ui.Context) *ui.Bitmap {
	style := a.prefs.Avatar
	if style == "custom" {
		return a.customAvatar
	}
	style = normalizedAvatarStyle(style)
	if style != a.avatarStyle {
		return nil
	}
	poses := a.avatarPoses
	if style == "bot" && len(poses) > 0 {
		c.After(80 * time.Millisecond)
		return poses[fmt.Sprintf("frame%02d", (c.Now().UnixMilli()/80)%100)]
	}
	if len(poses) == 0 {
		return nil
	}
	pose := "idle"
	phase := c.Now().Unix() % 16
	if phase >= 11 && phase < 13 {
		pose = "proud"
	} else if phase >= 13 && phase < 15 {
		pose = "stretch"
	}
	if poses[pose] == nil {
		pose = "idle"
	}
	if poses["proud"] != nil {
		c.After(time.Second)
	}
	return poses[pose]
}

func init() {
	for _, weight := range []int{400, 500, 600, 700} {
		data, err := resources.Assets.ReadFile(fmt.Sprintf("fonts/Inter-%d.ttf", weight))
		if err == nil {
			_ = ui.RegisterFont(data, "Inter")
		}
	}
}

func releaseAvatarPoses(poses map[string]*ui.Bitmap) {
	for _, bitmap := range poses {
		if bitmap != nil {
			bitmap.ReleasePixels()
		}
	}
}
