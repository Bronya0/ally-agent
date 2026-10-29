// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Optional real-browser validation of the embedded bundle, CSP and rendering.
func TestMermaidBrowser(t *testing.T) {
	browser := os.Getenv("ALLY_MERMAID_BROWSER")
	if browser == "" {
		t.Skip("set ALLY_MERMAID_BROWSER to a Chromium executable")
	}
	for _, tc := range []struct {
		name, source  string
		dark, invalid bool
		html          bool
	}{
		{"flowchart-dark", "flowchart TD\nU[点工具条按钮] --> C{Clicked?}\nC -->|是| L[ui.Local 翻转开合]\nL --> P[popoverPanel 弹出面板]\nP --> S[选一项]\nS --> D[dismiss 关闭]\nP -->|Esc 或点外部| D", true, false, false},
		{"sequence-light", "sequenceDiagram\n用户->>服务: 你好\n服务-->>用户: 完成", false, false, false},
		{"invalid", "flowchart TD\nA[", false, true, false},
		{"echarts", `<div id="chart" style="width:600px;height:350px"></div><script>const chart=echarts.init(document.getElementById('chart'),null,{renderer:'svg'});chart.setOption({animation:false,xAxis:{data:['A','B']},yAxis:{},series:[{type:'bar',data:[10,20]}]});</script>`, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "diagram.html")
			document := mermaidDocument(tc.source, tc.dark)
			if tc.html {
				document = toolHTMLDocument(tc.source)
			}
			if err := os.WriteFile(path, []byte(previewReadyDocument(document, "browser-test-ready", !tc.html)), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			u := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}
			out, err := exec.CommandContext(ctx, browser, "--headless", "--disable-gpu", "--no-first-run", "--disable-extensions", "--user-data-dir="+filepath.Join(dir, "profile"), "--dump-dom", "--virtual-time-budget=8000", u.String()).Output()
			if err != nil {
				t.Fatalf("browser: %v", err)
			}
			doc := string(out)
			if !strings.Contains(doc, "<title>browser-test-ready</title>") {
				t.Fatal("render completion was not signaled")
			}
			// Script source also contains SVG strings; inspect only the live body
			// before the first script element, where the renderer inserts its result.
			body := doc[strings.Index(doc, "<body"):]
			body = strings.SplitN(body, "<script", 2)[0]
			if tc.invalid {
				if !strings.Contains(body, "图表渲染失败") {
					t.Fatalf("missing visible error: %.1000s", body)
				}
			} else if !strings.Contains(body, "<svg") || strings.Contains(body, "图表渲染失败") {
				t.Fatalf("diagram did not render: %.1000s", body)
			}
		})
	}
}
