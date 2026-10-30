// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Documents belong to live previews. The protocol handler never captures a
// document or native view; deleting its URL releases the application reference.
var previewDocuments sync.Map

func storePreviewDocument(document string) string {
	key := uuid.NewString()
	previewDocuments.Store(key, document)
	return "ally-preview://document/" + key
}
func releasePreviewDocument(url string) {
	previewDocuments.Delete(strings.TrimPrefix(url, "ally-preview://document/"))
}
func servePreviewDocument(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host != "document" || r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	document, ok := previewDocuments.Load(strings.TrimPrefix(r.URL.Path, "/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, document.(string))
}
