// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"context"
	"fmt"
	"github.com/egoist/mygo/ui"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHTMLPreviewWebViewIntegration(t *testing.T) {
	if os.Getenv("ALLY_WEBVIEW_TEST") != "1" {
		t.Skip("native WebView integration")
	}
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	html := toolHTMLDocument(`<button id="counter" onclick="this.textContent=Number(this.textContent)+1">0</button><div id="chart" style="width:500px;height:200px"></div><script>echarts.init(document.getElementById('chart')).setOption({xAxis:{data:['A','B']},yAxis:{},series:[{type:'bar',data:[1,2]}]})</script>`)
	helper := `package main
import("context";"fmt";"os";"time";"github.com/egoist/mygo";"github.com/egoist/mygo/ui")
func main(){var view *mygo.WebView;result:=make(chan error,1)
mygo.App.WhenReady(func(){w:=mygo.NewWindow(mygo.WindowOptions{Title:"Interactive preview check",Hidden:true,Width:800,Height:500,Content:ui.View(func(c *ui.Context){if view!=nil{ui.WebView(c,view).Fill()}})})
v,e:=w.NewWebView(mygo.WebViewOptions{BackgroundColor:"#1b1b1b"});if e!=nil{result<-e;mygo.App.Quit();return};view=v
v.Page().OnDidFinishLoad(func(){go func(){ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel();value,err:=v.Page().EvalContext(ctx,"document.getElementById('counter').click(); return document.getElementById('counter').textContent === '1' && document.querySelectorAll('canvas').length === 1 && location.href.startsWith('https://ally-tool-preview.invalid/')");if err==nil && value!=true{err=fmt.Errorf("interaction failed: %%v",value)};result<-err;mygo.App.Quit()}()})
v.Page().LoadHTML(%q,"https://ally-tool-preview.invalid/")})
time.AfterFunc(25*time.Second,func(){select{case result<-fmt.Errorf("timeout"):default:};mygo.App.Quit()})
if err:=mygo.App.Run();err!=nil{fmt.Println(err);os.Exit(1)};if err:=<-result;err!=nil{fmt.Println(err);os.Exit(1)};fmt.Println("WebView interaction and ECharts verified")}
`
	path := filepath.Join(dir, "preview-check.go")
	if err := os.WriteFile(path, []byte(fmt.Sprintf(helper, html)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "go", "run", path).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	t.Log(string(output))
}

func TestInputHistoryRestoresDraftAndDoesNotStealMultilineArrows(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Messages = append(session.Messages, map[string]any{"role": "user", "content": "second"})
	if _, ok := navigateInputHistory(session, "unfinished\nmessage", true); ok {
		t.Fatal("up stole normal caret navigation")
	}
	if text, ok := navigateInputHistory(session, "", true); !ok || text != "second" {
		t.Fatal("up did not recall latest prompt")
	}
	if text, ok := navigateInputHistory(session, "second", false); !ok || text != "" {
		t.Fatal("down did not restore draft")
	}
}

func TestManualScrollStopsFollowDuringToolUpdates(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Busy = true
	for n := 0; n < 50; n++ {
		session.Messages = append(session.Messages, map[string]any{"role": "assistant", "content": fmt.Sprintf("message %d", n)})
	}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	session.Scroll.Y = session.Scroll.MaxY
	tt.Frame()
	tt.Scroll(500, 300, 0, -180)
	tt.Frame()
	if session.uiState().Follow {
		t.Fatal("manual scroll kept following")
	}
	before := session.Scroll.Y
	a.applyToolEvent(session, "tool:update", map[string]any{"toolCallId": "scroll-test", "name": "command", "args": `{"command":"echo streaming"}`})
	tt.Frame()
	if session.Scroll.Y > before+5 {
		t.Fatal("tool update forced scroll to bottom")
	}
}

func TestComposerKeyboardHistoryRoundTrip(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Messages = []map[string]any{{"role": "user", "content": "first prompt"}, {"role": "user", "content": "second prompt"}}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("消息输入框"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if session.Draft != "second prompt" {
		t.Fatalf("latest: %q", session.Draft)
	}
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	if session.Draft != "first prompt" {
		t.Fatalf("older: %q", session.Draft)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if session.Draft != "second prompt" {
		t.Fatalf("newer: %q", session.Draft)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if session.Draft != "" {
		t.Fatalf("draft: %q", session.Draft)
	}
}

func TestToolHeaderKeepsMetadataAndReadGroupAtLeft(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Messages = []map[string]any{{"role": "tool_call", "toolCallId": "header", "name": "command", "status": "success", "args": `{"command":"go test very-long-command-parameter very-long-command-parameter very-long-command-parameter very-long-command-parameter"}`, "result": `{"ok":true,"data":{"exitCode":0,"durationMs":1234}}`}, {"role": "tool_call", "toolCallId": "read", "name": "read", "status": "success", "args": `{"path":"file.go"}`}}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		ui.Column(c).Fill().Padding(20).Children(func() {
			for n := range session.Messages {
				a.messageRow(c, session, n)
			}
		})
	}, 500, 400)
	tt.Frame()
	exit, ok := tt.Find("exit 0")
	if !ok || exit.W < 25 || exit.H > 24 || exit.X+exit.W > 480 {
		t.Fatalf("compressed exit: %#v", exit)
	}
	group, ok := tt.Find("已读取 1 次")
	if !ok || group.X > 24 {
		t.Fatalf("group is centered: %#v", group)
	}
}

func TestHTTPStaysInlineAndCreateHasGutter(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Messages = []map[string]any{
		{"role": "tool_call", "name": "http_request", "status": "success", "toolCallId": "http", "args": `{"url":"https://example.com"}`, "result": `{"ok":true,"data":{"status":200,"bytesRead":1024,"body":"MUST_NOT_RENDER_BODY"}}`},
		{"role": "tool_call", "name": "create", "status": "success", "toolCallId": "source", "args": `{"path":"hello.go","content":"package main\nfunc main() {}"}`},
	}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if tt.HasText("MUST_NOT_RENDER_BODY") || !tt.HasText("200") {
		t.Fatalf("network summary wrong: %q", tt.Texts())
	}
	if !tt.HasText("1\n2") {
		t.Fatal("create gutter missing")
	}
}

func TestFollowSurvivesProgrammaticBottomRequest(t *testing.T) {
	a := demoDesktop()
	session := a.current()
	session.Busy = true
	for n := 0; n < 40; n++ {
		session.Messages = append(session.Messages, map[string]any{"role": "assistant", "content": fmt.Sprintf("line %d", n)})
	}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	session.uiState().Follow = true
	session.uiState().FollowPending = true
	for n := 0; n < 4; n++ {
		tt.Frame()
	}
	if !session.uiState().Follow {
		t.Fatal("programmatic scroll mistaken for manual scroll")
	}
}
