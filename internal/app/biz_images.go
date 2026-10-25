// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"ally-dev/internal/tools/imagegen"
	"github.com/google/uuid"
	openai "github.com/sashabaranov/go-openai"
)

const generatedImageHistoryMarker = "Generated image: "

var imageRefPattern = regexp.MustCompile(`^[a-f0-9]{64}/[a-f0-9]{32}$`)

func isGeneratedImageHistory(content string) bool {
	if len(content) > 2048 || !strings.HasPrefix(content, generatedImageHistoryMarker+"image:") {
		return false
	}
	key := strings.Fields(strings.TrimPrefix(content, generatedImageHistoryMarker+"image:"))
	return len(key) > 0 && imageRefPattern.MatchString(key[0])
}

// Originals are durable user artifacts, separate from disposable previews and
// compacted model history. Each edit creates a new record; no output is replaced.
type GeneratedImage struct {
	Ref        string   `json:"imageRef"`
	URL        string   `json:"url"`
	Mime       string   `json:"mime"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	Bytes      int      `json:"bytes"`
	SessionID  string   `json:"sessionId"`
	Model      string   `json:"model,omitempty"`
	Provider   string   `json:"provider,omitempty"`
	Prompt     string   `json:"prompt,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	SourceOnly bool     `json:"sourceOnly,omitempty"`
	CreatedAt  int64    `json:"createdAt"`
}

func imageSessionKey(sessionID string) string {
	h := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(h[:])
}

func (a *App) imagesDir() string { return filepath.Join(a.AppDataDir(), "generated-images") }

func generatedImageExtension(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	default:
		return ""
	}
}

func generatedImageFilename(ref, mime string) string {
	return filepath.Base(ref) + generatedImageExtension(mime)
}

func (a *App) saveGeneratedImage(data []byte, meta GeneratedImage) (GeneratedImage, error) {
	mime, width, height, err := imagegen.Inspect(data)
	if err != nil {
		return GeneratedImage{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return GeneratedImage{}, err
	}
	key := imageSessionKey(meta.SessionID) + "/" + strings.ReplaceAll(id.String(), "-", "")
	meta.Ref, meta.URL = "image:"+key, "/generated-images/"+key
	meta.Mime, meta.Width, meta.Height, meta.Bytes = mime, width, height, len(data)
	meta.CreatedAt = time.Now().UnixMilli()
	path := filepath.Join(a.imagesDir(), filepath.FromSlash(key))
	originalPath := path + generatedImageExtension(mime)
	// Keep provider bytes intact: these are originals, not disposable previews.
	if err = writeAtomicBytes(originalPath, data, 0o600); err != nil {
		return GeneratedImage{}, err
	}
	encoded, err := json.Marshal(meta)
	if err == nil {
		err = writeAtomicBytes(path+".json", encoded, 0o600)
	}
	if err != nil {
		_ = os.Remove(originalPath)
		return GeneratedImage{}, err
	}
	return meta, nil
}

func (a *App) readGeneratedImage(ref string) ([]byte, GeneratedImage, error) {
	key := strings.TrimPrefix(ref, "image:")
	if !strings.HasPrefix(ref, "image:") || !imageRefPattern.MatchString(key) {
		return nil, GeneratedImage{}, errors.New("invalid image reference")
	}
	root, err := os.OpenRoot(a.imagesDir())
	if err != nil {
		return nil, GeneratedImage{}, err
	}
	defer root.Close()
	metaData, err := root.ReadFile(filepath.FromSlash(key) + ".json")
	var meta GeneratedImage
	if err == nil {
		err = json.Unmarshal(metaData, &meta)
	}
	if err != nil {
		return nil, meta, err
	}
	ext := generatedImageExtension(meta.Mime)
	if ext == "" {
		return nil, meta, errors.New("invalid stored image MIME type")
	}
	path := filepath.FromSlash(key)
	f, err := root.Open(path + ext)
	if errors.Is(err, os.ErrNotExist) {
		f, err = root.Open(path + ".img")
		if errors.Is(err, os.ErrNotExist) {
			// Startup migration may have renamed it between the two opens.
			f, err = root.Open(path + ext)
		}
	}
	if err != nil {
		return nil, meta, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, imagegen.MaxImageBytes+1))
	if err == nil {
		_, _, _, err = imagegen.Inspect(data)
	}
	return data, meta, err
}

// Migrate outside the startup critical path. Link-before-unlink never replaces
// an existing original; interruption leaves at least one complete copy. Older
// or read-only libraries remain readable through readGeneratedImage's fallback.
func (a *App) migrateLegacyImageFiles(ctx context.Context) error {
	root, err := os.OpenRoot(a.imagesDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		key := strings.TrimSuffix(path, ".img")
		if !entry.Type().IsRegular() || key == path || !imageRefPattern.MatchString(key) {
			return nil
		}
		data, meta, err := a.readGeneratedImage("image:" + key)
		if err != nil {
			return nil // Leave incomplete or invalid legacy records untouched.
		}
		mime, _, _, err := imagegen.Inspect(data)
		if err != nil || mime != meta.Mime {
			return nil
		}
		source := filepath.FromSlash(path)
		target := filepath.FromSlash(key) + generatedImageExtension(mime)
		if err := root.Link(source, target); err != nil {
			if !errors.Is(err, os.ErrExist) {
				a.logAppError("image filename migration failed", "ref", "image:"+key, "error", err.Error())
			}
			return nil
		}
		if err := root.Remove(source); err != nil {
			a.logAppError("legacy image filename cleanup failed", "ref", "image:"+key, "error", err.Error())
		}
		return nil
	})
}

func (a *App) latestGeneratedImage(sessionID string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(a.imagesDir(), imageSessionKey(sessionID)))
	if err != nil {
		return "", errors.New("this session has no saved generated image")
	}
	var latest GeneratedImage
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.imagesDir(), imageSessionKey(sessionID), entry.Name()))
		var meta GeneratedImage
		if err == nil && json.Unmarshal(data, &meta) == nil && !meta.SourceOnly && meta.CreatedAt >= latest.CreatedAt {
			latest = meta
		}
	}
	if latest.Ref == "" {
		return "", errors.New("this session has no saved generated image")
	}
	return latest.Ref, nil
}

// GeneratedImageMiddleware serves only opaque, validated artifact references.
// Original file paths and arbitrary files are never exposed to the WebView.
func GeneratedImageMiddleware(a *App) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/generated-images/") {
				next.ServeHTTP(w, r)
				return
			}
			a.serveGeneratedImage(w, r, strings.TrimPrefix(r.URL.Path, "/generated-images/"))
		})
	}
}

func (a *App) serveGeneratedImage(w http.ResponseWriter, r *http.Request, key string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, meta, err := a.readGeneratedImage("image:" + key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", meta.Mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	if r.Method == http.MethodGet {
		_, _ = w.Write(data)
	}
}

func generatedImageDataURL(data []byte, mime string) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func (a *App) persistRunImageAttachments(req *ChatRequest, runID string) error {
	// Desktop requests carry attachments inside Messages; API callers may use
	// the top-level field. Handle both, owning the slices we annotate and saving
	// identical bytes only once within this request.
	saved := make(map[[sha256.Size]byte]GeneratedImage)
	persist := func(input []AttachmentInput) ([]AttachmentInput, error) {
		attachments := append([]AttachmentInput(nil), input...)
		for i := range attachments {
			att := &attachments[i]
			if !isImageAttachment(*att) || att.ImageRef != "" || !validImageDataURL(att.DataURL) {
				continue
			}
			if attachmentImageDataURLBytes(att.DataURL) > imagegen.MaxImageBytes {
				return nil, errors.New("reference image exceeds the size limit")
			}
			data, err := base64.StdEncoding.DecodeString(strings.SplitN(att.DataURL, ",", 2)[1])
			if err != nil {
				continue
			}
			// Other chat formats keep their existing behavior; only editing
			// formats receive a durable reference.
			if _, _, _, err := imagegen.Inspect(data); err != nil {
				continue
			}
			digest := sha256.Sum256(data)
			image, exists := saved[digest]
			if !exists {
				image, err = a.saveGeneratedImage(data, GeneratedImage{SessionID: req.SessionID, SourceOnly: true})
				if err != nil {
					return nil, err
				}
				saved[digest] = image
			}
			att.ImageRef = image.Ref
			a.emit("run:attachment", map[string]any{"runId": runID, "sessionId": req.SessionID, "id": att.ID, "imageRef": image.Ref, "url": image.URL})
		}
		return attachments, nil
	}
	var err error
	if req.Attachments, err = persist(req.Attachments); err != nil {
		return err
	}
	req.Messages = append([]ChatMessageInput(nil), req.Messages...)
	for i := range req.Messages {
		message := &req.Messages[i]
		if strings.TrimSpace(message.Role) != openai.ChatMessageRoleUser {
			continue
		}
		if message.Attachments, err = persist(message.Attachments); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) emitStreamImage(runID, sessionID, model string, image *modelImage) {
	payload := map[string]any{"runId": runID, "sessionId": sessionID, "id": image.ID, "dataUrl": image.DataURL, "mimeType": image.MimeType, "partial": image.Partial}
	if !image.Partial {
		var stored GeneratedImage
		parts := strings.SplitN(image.DataURL, ",", 2)
		var data []byte
		var err error
		if len(parts) != 2 {
			err = errors.New("invalid generated image")
		} else {
			data, err = base64.StdEncoding.DecodeString(parts[1])
		}
		if err == nil {
			stored, err = a.saveGeneratedImage(data, GeneratedImage{SessionID: sessionID, Model: model})
		}
		if err != nil {
			payload["error"] = "Could not save original: " + err.Error()
			a.logAppError("save generated original failed", "sessionId", sessionID, "error", err.Error())
		} else {
			payload["imageRef"], payload["url"], payload["bytes"] = stored.Ref, stored.URL, stored.Bytes
			delete(payload, "dataUrl")
		}
	}
	a.emit("run:image", payload)
}

// A successful tool result can reach disk before the UI snapshot does (crash,
// headless run). Restore only references present in history, so removed turns
// are never resurrected by scanning all saved artifacts.
func (a *App) restoreGeneratedImageAttachments(snapshot *SessionSnapshot, history []openai.ChatCompletionMessage) {
	type imageOwner struct {
		message    map[string]any
		attachment map[string]any
	}
	owners := make(map[string]imageOwner)
	cards := make(map[string]map[string]any)
	for _, message := range snapshot.Messages {
		if message["role"] == "tool_call" {
			if id, ok := message["toolCallId"].(string); ok && id != "" {
				cards[id] = message
			}
		}
		if attachments, ok := message["attachments"].([]any); ok {
			for _, raw := range attachments {
				if att, ok := raw.(map[string]any); ok {
					if ref, ok := att["imageRef"].(string); ok && (message["role"] == "tool_call" || message["role"] == "assistant" && att["generated"] == true) {
						owners[ref] = imageOwner{message, att}
					}
				}
			}
		}
	}
	calls := make(map[string]openai.ToolCall)
	for _, message := range history {
		for _, call := range message.ToolCalls {
			if call.Function.Name == "image_generate" || call.Function.Name == "image_edit" {
				calls[call.ID] = call
			}
		}
	}
	for _, message := range history {
		if message.Role != openai.ChatMessageRoleTool || !isGeneratedImageHistory(message.Content) {
			continue
		}
		ref := strings.Fields(strings.TrimPrefix(message.Content, generatedImageHistoryMarker))[0]
		owner := owners[ref]
		card := cards[message.ToolCallID]
		if owner.message != nil && owner.message["role"] == "tool_call" {
			continue
		}
		_, meta, err := a.readGeneratedImage(ref)
		if err != nil || meta.SessionID != snapshot.ID {
			continue
		}
		if card == nil {
			call := calls[message.ToolCallID]
			name := call.Function.Name
			if name == "" {
				name = "image_generate"
				if len(meta.Sources) > 0 {
					name = "image_edit"
				}
			}
			card = map[string]any{"role": "tool_call", "eventId": message.ToolCallID, "toolCallId": message.ToolCallID, "name": name, "kind": "image", "status": "success", "title": meta.Prompt, "body": fmt.Sprintf("%d × %d", meta.Width, meta.Height)}
			snapshot.Messages = append(snapshot.Messages, card)
			cards[message.ToolCallID] = card
		}
		if title, _ := card["title"].(string); strings.TrimSpace(title) == "" {
			card["title"] = meta.Prompt
		}
		attachment := owner.attachment
		if attachment == nil {
			attachment = map[string]any{"id": ref, "imageRef": ref, "url": meta.URL, "name": "", "type": meta.Mime, "size": meta.Bytes, "width": meta.Width, "height": meta.Height, "kind": "image", "generated": true}
		} else {
			// Migrate older snapshots whose tool output was appended to an
			// assistant row. Keep the thumbnail, but put it on its own call.
			attachments := owner.message["attachments"].([]any)
			kept := make([]any, 0, len(attachments))
			for _, raw := range attachments {
				if att, ok := raw.(map[string]any); !ok || att["imageRef"] != ref {
					kept = append(kept, raw)
				}
			}
			owner.message["attachments"] = kept
		}
		attachments, _ := card["attachments"].([]any)
		card["attachments"] = append(attachments, attachment)
		owners[ref] = imageOwner{card, attachment}
	}
}
