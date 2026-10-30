// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreviewDocumentIsRevocable(t *testing.T) {
	document := strings.Repeat("document", 1024*1024)
	url := storePreviewDocument(document)
	request := func(url string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		servePreviewDocument(w, httptest.NewRequest("GET", url, nil))
		return w
	}
	if w := request(url); w.Code != 200 || w.Body.String() != document || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("live document unavailable")
	}
	other := storePreviewDocument("other")
	defer releasePreviewDocument(other)
	releasePreviewDocument(url)
	releasePreviewDocument(url)
	if request(url).Code != 404 {
		t.Fatal("retired large document still reachable")
	}
	if request(other).Body.String() != "other" {
		t.Fatal("unrelated document removed")
	}
}
