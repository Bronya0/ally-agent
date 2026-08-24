package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ally-dev/internal/tools/shared"
)

func webFetchTestConfig() ConfigState {
	return ConfigState{AllowPrivateNetwork: true}
}

// readable 模式：结构化文章页应提取出干净正文，且不含导航噪声。
func TestWebFetchReadableExtractsArticle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><head><title>测试文章 | 示例站</title></head><body>
<nav><a href="/home">首页</a> <a href="/about">关于</a></nav>
<article><h1>Go 语言并发入门</h1>
<p>` + strings.Repeat("并发编程的核心是 goroutine 与 channel。", 30) + `</p>
<pre><code>go func() { ch &lt;- 1 }()</code></pre>
</article>
<footer>版权所有</footer>
</body></html>`))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Title, "Go 语言并发入门") && !strings.Contains(got.Title, "测试文章") {
		t.Fatalf("unexpected title: %q", got.Title)
	}
	if !strings.Contains(got.Text, "goroutine") || !strings.Contains(got.Text, "go func()") {
		t.Fatalf("expected article text with code block preserved, got: %.200q", got.Text)
	}
	if strings.Contains(got.Text, "版权所有") || strings.Contains(got.Text, "首页") {
		t.Fatalf("nav/footer noise should be stripped, got: %q", got.Text)
	}
}

// readable 模式：非文章页（纯导航）不会失败——v2 的 readerable 判定宽松，
// 会把整页文本当内容返回。记录该行为，防止误以为它会拒绝导航页。
func TestWebFetchReadableNonArticleFallsBackToPageText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>导航站</title></head><body>
<a href="/a">A</a> <a href="/b">B</a> <a href="/c">C</a></body></html>`))
	}))
	defer server.Close()

	app := NewApp()
	// 非文章页（纯导航）Readability 不会报错，而是返回近似全页的文本；
	// 断言降级产物：title 正确、正文为链接锚文本、links 完整。
	got, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "导航站" {
		t.Fatalf("unexpected title: %q", got.Title)
	}
	if strings.TrimSpace(got.Text) != "A B C" {
		t.Fatalf("expected fallback link text as content, got %q", got.Text)
	}
	if len(got.Links) != 3 {
		t.Fatalf("expected all 3 links collected, got %+v", got.Links)
	}
}

// raw 模式：返回解码后的源码（含 script 内容），HTML 页仍带 <title> 和链接。
func TestWebFetchRawReturnsSourceWithMeta(t *testing.T) {
	const scriptBody = "var marker = 'script-content-marker';"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>原始页面</title><script>` + scriptBody + `</script></head>
<body><p>hello</p><a href="/x">X 链接</a><a href="https://ext.example/y">Y</a></body></html>`))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, scriptBody) {
		t.Fatalf("raw text should contain unextracted source (script body), got %q", got.Text)
	}
	if got.Title != "原始页面" {
		t.Fatalf("raw mode should still collect <title>, got %q", got.Title)
	}
	if len(got.Links) != 2 || got.Links[0].Text != "X 链接" {
		t.Fatalf("raw mode should still collect links, got %+v", got.Links)
	}
	for _, l := range got.Links {
		if !strings.HasPrefix(l.URL, "http://127.0.0.1") && !strings.HasPrefix(l.URL, "https://ext.example/") {
			t.Fatalf("link not resolved to base URL: %+v", l)
		}
	}
}

// raw 对纯文本响应同样生效，且不报错。
func TestWebFetchRawPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("plain text content\nline two\n"))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Text, "line two") {
		t.Fatalf("expected plain text body, got %q", got.Text)
	}
}

// format 归一化：大小写/空白容忍，非法值报 E_HTTP_BAD_FORMAT。
func TestWebFetchFormatValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	app := NewApp()
	if _, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: " RAW "}); err != nil {
		t.Fatalf("RAW should be accepted after normalization: %v", err)
	}
	_, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: "markdown"})
	if err == nil {
		t.Fatal("expected invalid-format error")
	}
	var coded shared.CodedError
	if !errors.As(err, &coded) || coded.ToolErrorCode() != "E_HTTP_BAD_FORMAT" {
		t.Fatalf("expected E_HTTP_BAD_FORMAT, got %v", err)
	}
}

// maxChars 截断对 raw 同样生效。
func TestWebFetchRawRespectsMaxChars(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("a", 5000)))
	}))
	defer server.Close()

	app := NewApp()
	got, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: "raw", MaxChars: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Truncated || len([]rune(got.Text)) > 103 {
		t.Fatalf("expected truncation to ~100 chars, got truncated=%v len=%d", got.Truncated, len([]rune(got.Text)))
	}
}

// 二进制响应在 raw 模式下仍拒绝。
func TestWebFetchRawRejectsBinary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x00, 0x01, 0xFF, 0xD8})
	}))
	defer server.Close()

	app := NewApp()
	_, err := app.webFetchToolWithConfig(context.Background(), webFetchTestConfig(), WebFetchRequest{URL: server.URL, Format: "raw"})
	if err == nil {
		t.Fatal("expected binary rejection in raw mode")
	}
	var coded shared.CodedError
	if !errors.As(err, &coded) || coded.ToolErrorCode() != "E_WEB_FETCH_NOT_TEXT" {
		t.Fatalf("expected E_WEB_FETCH_NOT_TEXT, got %v", err)
	}
}
