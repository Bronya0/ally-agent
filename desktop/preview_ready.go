// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"strings"
)

// Readiness means fonts and the first chart frame, rather than DOM load alone.
// The timer fallback also works while the native loading cover occludes the page
// (Chromium may suspend requestAnimationFrame for fully covered WebViews).
func previewReadyDocument(document, token string, mermaid bool) string {
	encoded, _ := json.Marshal(token)
	signal := `<script>(()=>{let done=false;window.__allyPreviewReady=async()=>{if(done)return;done=true;try{await document.fonts.ready;}catch(_){}const reveal=()=>{document.title=` + string(encoded) + `;};let shown=false;const once=()=>{if(shown)return;shown=true;reveal();};setTimeout(once,120);requestAnimationFrame(()=>requestAnimationFrame(once));};`
	if !mermaid {
		signal += `window.addEventListener('load',()=>window.__allyPreviewReady(),{once:true});`
	}
	signal += `})();</script>`
	if at := strings.Index(document, "<head>"); at >= 0 {
		at += len("<head>")
		return document[:at] + signal + document[at:]
	}
	return signal + document
}
