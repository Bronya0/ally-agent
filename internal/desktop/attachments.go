// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	backend "ally-dev/internal/app"
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/google/uuid"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const maxAttachments = 8
const maxAttachmentTextBytes = 200 * 1024
const maxModelImageBytes = 5 * 1024 * 1024
const maxOriginalImageBytes = 64 * 1024 * 1024

var textAttachmentExt = map[string]bool{}

func init() {
	for _, ext := range strings.Fields("txt md markdown json jsonl xml html htm css js mjs cjs ts tsx jsx vue go py java c cc cpp h hpp cs rs rb php sh bash zsh ps1 bat cmd sql yaml yml toml ini env log csv tsv") {
		textAttachmentExt["."+ext] = true
	}
}

type preparedAttachment struct {
	input   backend.AttachmentInput
	preview image.Image
}

// prepareAttachment runs off the UI thread and only reads a bounded amount of
// each chosen file. Errors remain visible on the attachment card and are also
// passed to the backend, matching the original frontend's contract.
func prepareAttachment(path string) preparedAttachment {
	att := backend.AttachmentInput{ID: uuid.NewString(), Name: filepath.Base(path), FilePath: path}
	result := preparedAttachment{input: att}
	f, err := os.Open(path)
	if err != nil {
		result.input.Error = err.Error()
		return result
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		result.input.Error = err.Error()
		return result
	}
	if !info.Mode().IsRegular() {
		result.input.Error = "请选择普通文件"
		return result
	}
	result.input.Size = info.Size()
	ext := strings.ToLower(filepath.Ext(path))
	typ := mime.TypeByExtension(ext)
	if i := strings.IndexByte(typ, ';'); i >= 0 {
		typ = typ[:i]
	}
	result.input.Type = typ
	isImage := strings.HasPrefix(typ, "image/") || ext == ".webp" || ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".gif"
	if isImage {
		result.input.Kind = "image"
		if info.Size() > maxOriginalImageBytes {
			result.input.Error = "图片过大，无法读取（上限 64 MB）"
			return result
		}
		data, err := io.ReadAll(io.LimitReader(f, maxOriginalImageBytes+1))
		if err != nil {
			result.input.Error = err.Error()
			return result
		}
		return prepareImageData(result, data)
	}
	if textAttachmentExt[ext] || strings.HasPrefix(typ, "text/") || strings.HasSuffix(typ, "+json") || strings.HasSuffix(typ, "+xml") || typ == "application/json" || typ == "application/xml" {
		result.input.Kind = "text"
		if info.Size() > maxAttachmentTextBytes {
			result.input.Truncated = true
			result.input.Error = "文本附件超过 200 KB，无法发送"
			return result
		}
		data, err := io.ReadAll(io.LimitReader(f, maxAttachmentTextBytes+1))
		if err != nil {
			result.input.Error = err.Error()
			return result
		}
		if !utf8.Valid(data) {
			result.input.Error = "文本不是有效的 UTF-8 编码"
			return result
		}
		result.input.Text = string(data)
		return result
	}
	result.input.Kind = "file"
	result.input.Error = "暂不支持此类型的附件；请选择图片或文本文件"
	return result
}
func prepareImageData(result preparedAttachment, data []byte) preparedAttachment {
	if len(data) > maxOriginalImageBytes {
		result.input.Error = "图片过大，无法读取（上限 64 MB）"
		return result
	}
	var typ string
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		result.input.Error = "无法读取图片：" + err.Error()
		return result
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 64*1024*1024 {
		result.input.Error = "图片尺寸过大，无法解码"
		return result
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		result.input.Error = "无法读取图片：" + err.Error()
		return result
	}
	result.preview = resizeImage(img, 256)
	switch format {
	case "jpeg":
		typ = "image/jpeg"
	case "png":
		typ = "image/png"
	case "webp":
		typ = "image/webp"
	default:
		typ = ""
	}
	if typ == "" || len(data) > 256*1024 {
		var out bytes.Buffer
		err = jpeg.Encode(&out, resizeImage(img, 2048), &jpeg.Options{Quality: 70})
		if err != nil {
			result.input.Error = "图片转换失败：" + err.Error()
			return result
		}
		data = out.Bytes()
		typ = "image/jpeg"
	}
	if len(data) > maxModelImageBytes {
		result.input.Error = "压缩后的图片超过 5 MB，无法发送"
		return result
	}
	result.input.DataURL = "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(data)
	return result
}

func resizeImage(src image.Image, limit int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= limit && h <= limit {
		return src
	}
	if w >= h {
		h = max(1, h*limit/w)
		w = limit
	} else {
		w = max(1, w*limit/h)
		h = limit
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	return dst
}
func (a *desktop) chooseAttachments(t *conversation) {
	if t.AttachmentLoading || t.Busy {
		return
	}
	t.AttachmentLoading = true
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: "添加附件", Multiple: true})
		if err != nil {
			a.win.Update(func() { t.AttachmentLoading = false; a.notify(err.Error()) })
			return
		}
		a.prepareAttachmentPaths(t, paths)
	}()
}

// Called only after AttachmentLoading is set on the UI thread.
func (a *desktop) prepareAttachmentPaths(t *conversation, paths []string) {
	prepared := make([]preparedAttachment, 0, min(len(paths), maxAttachments))
	for _, p := range paths {
		if len(prepared) >= maxAttachments {
			break
		}
		prepared = append(prepared, prepareAttachment(p))
	}
	a.win.Update(func() { a.finishAttachments(t, prepared, len(paths)) })
}
func (a *desktop) dropAttachments(t *conversation, paths []string) {
	if t.AttachmentLoading || t.Busy || a.closing {
		return
	}
	t.AttachmentLoading = true
	copyPaths := append([]string(nil), paths...)
	go a.prepareAttachmentPaths(t, copyPaths)
}
func (a *desktop) pasteAttachment(t *conversation, data []byte) {
	if t.AttachmentLoading || t.Busy || a.closing {
		return
	}
	t.AttachmentLoading = true
	go func() {
		input := backend.AttachmentInput{ID: uuid.NewString(), Name: "剪贴板图片.png", Type: "image/png", Kind: "image", Size: int64(len(data))}
		prepared := prepareImageData(preparedAttachment{input: input}, data)
		a.win.Update(func() { a.finishAttachments(t, []preparedAttachment{prepared}, 1) })
	}()
}
func (a *desktop) finishAttachments(t *conversation, prepared []preparedAttachment, count int) {
	t.AttachmentLoading = false
	if a.closing {
		return
	}
	for _, p := range prepared {
		if len(t.PendingAttachments) >= maxAttachments {
			a.status = "每条消息最多添加 8 个附件"
			break
		}
		t.PendingAttachments = append(t.PendingAttachments, p.input)
		if p.preview != nil {
			ensureAttachmentMaps(t)
			t.AttachmentImages[p.input.ID] = ui.NewBitmap(p.preview)
		}
		if p.input.Error != "" {
			a.status = p.input.Error
		}
	}
	if count > maxAttachments {
		a.status = "每条消息最多添加 8 个附件"
	}
}

func ensureAttachmentMaps(t *conversation) {
	if t.AttachmentImages == nil {
		t.AttachmentImages = map[string]*ui.Bitmap{}
	}
	if t.AttachmentImageVersions == nil {
		t.AttachmentImageVersions = map[string]uint64{}
	}
}
func (a *desktop) pendingAttachmentRow(c *ui.Context, t *conversation) {
	ui.Row(c).Gap(6).Wrap().Children(func() {
		for _, att := range t.PendingAttachments {
			ui.Row(c.Key(att.ID)).Padding(5, 8).Gap(5).Background(panel).Radius(5).Children(func() {
				if bm := t.AttachmentImages[att.ID]; bm != nil {
					ui.Image(c, bm).Size(32, 32).Fit(ui.Contain)
				}
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, short(att.Name, 18)).FontSize(11).Tooltip(att.Name)
					if att.Error != "" {
						ui.Text(c, short(att.Error, 18)).FontSize(10).TextColor(c.Theme().Danger).Tooltip(att.Error)
					}
				})
				flat(c, "×").Label("移除 " + att.Name).OnClick(func() {
					for i, item := range t.PendingAttachments {
						if item.ID == att.ID {
							t.PendingAttachments = append(t.PendingAttachments[:i], t.PendingAttachments[i+1:]...)
							delete(t.AttachmentImages, att.ID)
							break
						}
					}
				})
			})
		}
	})
}
func messageAttachmentList(m map[string]any) []map[string]any {
	raw, ok := m["attachments"].([]any)
	if !ok {
		return nil
	}
	list := make([]map[string]any, 0, len(raw))
	for _, v := range raw {
		if att, ok := v.(map[string]any); ok {
			list = append(list, att)
		}
	}
	return list
}
func (a *desktop) messageAttachments(c *ui.Context, t *conversation, m map[string]any) {
	for _, att := range messageAttachmentList(m) {
		id := str(att, "id")
		name := str(att, "name")
		if name == "" {
			name = "图片"
		}
		ui.Row(c.Key("attachment-" + id)).Gap(8).Padding(8).Background(panel).Radius(6).Children(func() {
			if bm := t.AttachmentImages[id]; bm != nil {
				ui.Image(c, bm).Size(96, 96).Fit(ui.Contain).Radius(4)
			}
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, name).FontSize(12).Bold()
				if e := str(att, "error"); e != "" {
					ui.Text(c, e).FontSize(11).TextColor(c.Theme().Danger)
				} else {
					ui.Text(c, str(att, "kind")).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
			if ref := str(att, "imageRef"); ref != "" {
				flat(c, "保存图片").OnClick(func() { a.job(func() error { _, err := a.core.ExportGeneratedImage(ref); return err }) })
			}
		})
	}
}
func (a *desktop) recordAttachmentReference(t *conversation, p map[string]any) {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		for _, att := range messageAttachmentList(t.Messages[i]) {
			if str(att, "id") == str(p, "id") {
				att["imageRef"] = str(p, "imageRef")
				att["url"] = str(p, "url")
				return
			}
		}
	}
}
func (a *desktop) recordGeneratedImage(t *conversation, p map[string]any) {
	id := str(p, "id")
	if id == "" {
		return
	}
	var m map[string]any
	if n := len(t.Messages); n > 0 && str(t.Messages[n-1], "role") == "assistant" && str(t.Messages[n-1], "toolCallId") == "" {
		m = t.Messages[n-1]
	} else {
		m = map[string]any{"role": "assistant", "content": ""}
		t.Messages = append(t.Messages, m)
	}
	var att map[string]any
	for _, item := range messageAttachmentList(m) {
		if str(item, "id") == id {
			att = item
			break
		}
	}
	if att == nil {
		att = map[string]any{"id": id, "name": "生成的图片", "kind": "image", "generated": true}
		m["attachments"] = appendAttachment(m["attachments"], att)
	}
	att["imageRef"] = str(p, "imageRef")
	att["url"] = str(p, "url")
	att["type"] = str(p, "mimeType")
	att["error"] = str(p, "error")
	if ref := str(p, "imageRef"); ref != "" {
		att["dataUrl"] = ""
		a.loadAttachmentImage(t, id, ref, "")
	} else if data := str(p, "dataUrl"); data != "" {
		att["dataUrl"] = data
		a.loadAttachmentImage(t, id, "", data)
	}
}
func appendAttachment(v any, att map[string]any) []any {
	items, _ := v.([]any)
	return append(items, att)
}
func (a *desktop) loadSessionAttachmentImages(t *conversation) {
	for _, m := range t.Messages {
		for _, att := range messageAttachmentList(m) {
			id := str(att, "id")
			if id != "" && str(att, "kind") == "image" {
				a.loadAttachmentImage(t, id, str(att, "imageRef"), str(att, "dataUrl"))
			}
		}
	}
}
func (a *desktop) loadAttachmentImage(t *conversation, id, ref, dataURL string) {
	if ref == "" && dataURL == "" {
		return
	}
	ensureAttachmentMaps(t)
	t.AttachmentImageVersions[id]++
	version := t.AttachmentImageVersions[id]
	go func() {
		var data []byte
		var err error
		if ref != "" {
			data, err = a.core.ReadNativeGeneratedImage(ref)
		} else if comma := strings.IndexByte(dataURL, ','); comma >= 0 && strings.HasPrefix(dataURL, "data:image/") {
			data, err = base64.StdEncoding.DecodeString(dataURL[comma+1:])
		} else {
			return
		}
		if err != nil || len(data) > maxOriginalImageBytes {
			return
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || int64(cfg.Width)*int64(cfg.Height) > 64*1024*1024 {
			return
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return
		}
		img = resizeImage(img, 256)
		a.win.Update(func() {
			if !a.closing && t.AttachmentImages != nil && t.AttachmentImageVersions[id] == version {
				t.AttachmentImages[id] = ui.NewBitmap(img)
			}
		})
	}()
}
