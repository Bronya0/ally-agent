// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"codeberg.org/readeck/go-readability/v2"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

type httpFetchResult struct {
	Result HTTPRequestToolResult
	Raw    []byte
}

func (a *App) httpRequestTool(ctx context.Context, req HTTPRequestToolRequest) (HTTPRequestToolResult, error) {
	return a.httpRequestToolWithConfig(ctx, a.effectiveConfigSafe(), req)
}

func (a *App) httpRequestToolWithConfig(ctx context.Context, cfg ConfigState, req HTTPRequestToolRequest) (HTTPRequestToolResult, error) {
	if strings.TrimSpace(req.SaveTo) != "" && req.MaxBytes <= 0 {
		req.MaxBytes = maxHTTPBodyBytes
	}
	allowPrivate := cfg.AllowPrivateNetwork
	if req.AllowPrivateNetwork != nil {
		allowPrivate = *req.AllowPrivateNetwork
	}
	fetched, err := a.doHTTPRequest(ctx, cfg, req, false, allowPrivate)
	if err != nil {
		return HTTPRequestToolResult{}, err
	}
	if strings.TrimSpace(req.SaveTo) != "" {
		roots, err := workspaceRoots(cfg)
		if err != nil {
			return HTTPRequestToolResult{}, err
		}
		path, err := resolveWritableFilePath(roots, req.SaveTo)
		if err != nil {
			return HTTPRequestToolResult{}, err
		}
		if err := safeWriteFile(path, fetched.Raw, 0o644); err != nil {
			return HTTPRequestToolResult{}, err
		}
		fetched.Result.SavedPath = filepath.ToSlash(req.SaveTo)
		fetched.Result.Body, fetched.Result.BodyBase64 = "", ""
		fetched.Result.JSON, fetched.Result.JSONPreview = nil, ""
	}
	return fetched.Result, nil
}

func (a *App) webFetchToolWithConfig(ctx context.Context, cfg ConfigState, req WebFetchRequest) (WebFetchResult, error) {
	if strings.TrimSpace(req.URL) == "" {
		return WebFetchResult{}, errors.New("url is required")
	}
	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = 60000
	}
	if maxChars > 200000 {
		maxChars = 200000
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" {
		format = webFetchFormatReadable
	}
	if format != webFetchFormatReadable && format != webFetchFormatRaw {
		return WebFetchResult{}, codedToolError("E_HTTP_BAD_FORMAT", fmt.Errorf("invalid format %q; use \"readable\" or \"raw\"", req.Format))
	}
	if req.MaxBytes <= 0 {
		req.MaxBytes = defaultWebFetchBody
	}
	allowPrivate := cfg.AllowPrivateNetwork
	if req.AllowPrivateNetwork != nil {
		allowPrivate = *req.AllowPrivateNetwork
	}
	fetched, err := a.doHTTPRequest(ctx, cfg, HTTPRequestToolRequest{
		Method:             "GET",
		URL:                req.URL,
		Headers:            mergeStringMaps(map[string]string{"Accept": "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5"}, req.Headers),
		TimeoutSeconds:     req.TimeoutSeconds,
		MaxBytes:           req.MaxBytes,
		FollowRedirects:    boolPtr(true),
		InsecureSkipVerify: req.InsecureSkipVerify,
	}, true, allowPrivate)
	if err != nil {
		return WebFetchResult{}, err
	}

	contentType := fetched.Result.ContentType
	htmlData := fetched.Raw
	if fetched.Result.BodyEncoding == "text" {
		htmlData = []byte(fetched.Result.Body)
	}

	title := ""
	text := ""
	links := []WebFetchLink{}
	switch {
	case format == webFetchFormatRaw:
		if fetched.Result.BodyEncoding != "text" {
			return WebFetchResult{}, codedToolError("E_WEB_FETCH_NOT_TEXT", fmt.Errorf("web_fetch expected readable text/html, got %q", contentType))
		}
		text = fetched.Result.Body
	case isHTMLContentType(contentType):
		var extractErr error
		title, text, links, extractErr = htmlExtractContent(htmlData, fetched.Result.FinalURL)
		if extractErr != nil {
			return WebFetchResult{}, codedToolError("E_WEB_FETCH_EXTRACT", fmt.Errorf("no readable article found on this page (%w); retry with \"format\":\"raw\" for the page source, or use http_request", extractErr))
		}
	case fetched.Result.BodyEncoding == "text":
		text = fetched.Result.Body
	default:
		return WebFetchResult{}, codedToolError("E_WEB_FETCH_NOT_TEXT", fmt.Errorf("web_fetch expected readable text/html, got %q", contentType))
	}

	if format == webFetchFormatRaw && isHTMLContentType(contentType) {
		baseURL, _ := url.Parse(fetched.Result.FinalURL)
		title, links = collectHTMLTitleAndLinks(htmlData, baseURL)
	}

	truncated := fetched.Result.Truncated
	if len([]rune(text)) > maxChars {
		text = truncateRunes(text, maxChars)
		truncated = true
	}

	return WebFetchResult{
		URL:         fetched.Result.URL,
		FinalURL:    fetched.Result.FinalURL,
		Status:      fetched.Result.Status,
		StatusText:  fetched.Result.StatusText,
		Title:       title,
		Text:        text,
		ContentType: contentType,
		Links:       links,
		BytesRead:   fetched.Result.BytesRead,
		Truncated:   truncated,
		DurationMS:  fetched.Result.DurationMS,
	}, nil
}

func (a *App) doHTTPRequest(parent context.Context, cfg ConfigState, req HTTPRequestToolRequest, preferText bool, allowPrivateNetwork bool) (httpFetchResult, error) {
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	if strings.ContainsAny(method, " \t\r\n") {
		return httpFetchResult{}, codedToolError("E_HTTP_BAD_METHOD", fmt.Errorf("invalid HTTP method %q", method))
	}
	target, err := normalizeHTTPRequestURL(req.URL, req.Query)
	if err != nil {
		return httpFetchResult{}, codedToolError("E_HTTP_BAD_URL", err)
	}
	if err := validateHTTPURLAccessForConfig(target, allowPrivateNetwork, cfg); err != nil {
		return httpFetchResult{}, err
	}

	timeout := req.TimeoutSeconds
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	if timeout > 120 {
		timeout = 120
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultHTTPMaxBody
	}
	if maxBytes > maxHTTPBodyBytes {
		maxBytes = maxHTTPBodyBytes
	}

	headers := normalizeHeaders(req.Headers)
	ua := headerValue(headers, "User-Agent")
	if ua == "" {
		ua = defaultHTTPUA
		headers["User-Agent"] = ua
	}
	if headerValue(headers, "Accept") == "" {
		if preferText {
			headers["Accept"] = "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5"
		} else {
			headers["Accept"] = "*/*"
		}
	}
	if headerValue(headers, "Accept-Language") == "" {
		headers["Accept-Language"] = "en-US,en;q=0.8"
	}
	if headerValue(headers, "Accept-Encoding") == "" {
		headers["Accept-Encoding"] = "gzip, deflate, br, zstd"
	}

	var body io.Reader
	if req.Body != "" && req.JSON != nil {
		return httpFetchResult{}, errors.New("body and json are mutually exclusive")
	}
	if req.JSON != nil {
		payload, err := json.Marshal(req.JSON)
		if err != nil {
			return httpFetchResult{}, fmt.Errorf("encode json body: %w", err)
		}
		body = bytes.NewReader(payload)
		if headerValue(headers, "Content-Type") == "" {
			headers["Content-Type"] = "application/json"
		}
	} else if req.Body != "" {
		body = strings.NewReader(req.Body)
	}

	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Second)
	defer cancel()

	redirects := []string{}
	followRedirects := boolDefault(req.FollowRedirects, true)
	client := &http.Client{
		Timeout:   time.Duration(timeout) * time.Second,
		Transport: httpTransport(cfg, allowPrivateNetwork, boolDefault(req.InsecureSkipVerify, false)),
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if !followRedirects {
				return http.ErrUseLastResponse
			}
			if len(via) >= 5 {
				return errors.New("stopped after 5 redirects")
			}
			if err := validateHTTPURLAccessForConfig(next.URL, allowPrivateNetwork, cfg); err != nil {
				return err
			}
			redirects = append(redirects, next.URL.String())
			previousURL := target
			if len(via) > 0 && via[len(via)-1] != nil && via[len(via)-1].URL != nil {
				previousURL = via[len(via)-1].URL
			}
			sameOrigin := sameHTTPOrigin(previousURL, next.URL)
			if !sameOrigin {
				stripSensitiveRedirectHeaders(next.Header)
			}
			for k, v := range headers {
				if strings.EqualFold(k, "Host") || (!sameOrigin && isSensitiveRedirectHeader(k)) {
					continue
				}
				if next.Header.Get(k) == "" {
					next.Header.Set(k, v)
				}
			}
			return nil
		},
	}

	if err := a.waitHTTPRateLimit(ctx, target); err != nil {
		return httpFetchResult{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return httpFetchResult{}, err
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Host") {
			httpReq.Host = v
			continue
		}
		httpReq.Header.Set(k, v)
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		return httpFetchResult{}, err
	}

	decodedBody, contentEncoding, err := decodedHTTPBody(resp)
	if err != nil {
		return httpFetchResult{}, err
	}
	defer decodedBody.Close()

	raw, truncated, err := readLimited(decodedBody, maxBytes)
	if err != nil {
		return httpFetchResult{}, err
	}
	contentType := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "" {
		mediaType = contentType
	}

	result := HTTPRequestToolResult{
		Method:      method,
		URL:         target.String(),
		FinalURL:    resp.Request.URL.String(),
		Status:      resp.StatusCode,
		StatusText:  resp.Status,
		Headers:     flattenHTTPHeaders(resp.Header),
		ContentType: mediaType,
		BytesRead:   len(raw),
		Truncated:   truncated,
		DurationMS:  duration,
		Redirects:   redirects,
	}
	if contentEncoding != "" {
		result.Headers["Ally-Decoded-Content-Encoding"] = contentEncoding
	}
	if isTextResponse(mediaType, raw) {
		result.Body = decodeHTTPText(raw, contentType)
		result.BodyEncoding = "text"
	} else {
		result.BodyBase64 = base64.StdEncoding.EncodeToString(raw)
		result.BodyEncoding = "base64"
	}
	if isJSONContentType(mediaType) {
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			result.JSON = parsed
			result.JSONPreview, result.JSONTruncated = previewJSON(parsed, maxHTTPJSONPreview)
			if truncated {
				result.JSONTruncated = true
			}
		}
	}
	return httpFetchResult{Result: result, Raw: raw}, nil
}

func sameHTTPOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectiveHTTPPort(a) == effectiveHTTPPort(b)
}

func effectiveHTTPPort(u *url.URL) string {
	if u == nil {
		return ""
	}
	if port := u.Port(); port != "" {
		return port
	}
	switch strings.ToLower(u.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func isSensitiveRedirectHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "cookie2",
		"x-api-key", "api-key", "x-auth-token", "x-access-token",
		"x-csrf-token", "x-xsrf-token":
		return true
	default:
		return false
	}
}

func stripSensitiveRedirectHeaders(headers http.Header) {
	for name := range headers {
		if isSensitiveRedirectHeader(name) {
			headers.Del(name)
		}
	}
}

func isJSONContentType(mediaType string) bool {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return mediaType == "application/json" ||
		mediaType == "text/json" ||
		strings.HasSuffix(mediaType, "+json")
}

func previewJSON(value any, limit int) (string, bool) {
	if value == nil || limit <= 0 {
		return "", false
	}
	pretty, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", false
	}
	if len(pretty) <= limit {
		return string(pretty), false
	}
	return truncateRunes(string(pretty), limit), true
}

func normalizeHTTPRequestURL(rawURL string, query map[string]string) (*url.URL, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, errors.New("url is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", parsed.Scheme)
	}
	if parsed.Hostname() == "" {
		return nil, errors.New("url must include a host")
	}
	if len(query) > 0 {
		q := parsed.Query()
		for k, v := range query {
			q.Set(k, v)
		}
		parsed.RawQuery = q.Encode()
	}
	return parsed, nil
}

func validateHTTPURLAccess(target *url.URL, allowPrivate bool) error {
	if target == nil {
		return errors.New("url is required")
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q; only http and https are allowed", target.Scheme)
	}
	host := target.Hostname()
	if host == "" {
		return errors.New("url must include a host")
	}
	if allowPrivate {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ipAddr := range ips {
		if isPrivateHTTPAddress(ipAddr.IP) {
			return fmt.Errorf("refusing private or local network address %s for host %s because allowPrivateNetwork=false", ipAddr.IP.String(), host)
		}
	}
	return nil
}

func validateHTTPURLAccessForConfig(target *url.URL, allowPrivate bool, cfg ConfigState) error {
	if normalizeProxyMode(cfg.ProxyMode) == proxyModeOff {
		return validateHTTPURLAccess(target, allowPrivate)
	}
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" {
		return validateHTTPURLAccess(target, allowPrivate)
	}
	if allowPrivate {
		return nil
	}
	if ip := net.ParseIP(target.Hostname()); ip != nil && isPrivateHTTPAddress(ip) {
		return fmt.Errorf("refusing private or local network address %s because allowPrivateNetwork=false", ip)
	}
	// Proxy DNS may resolve names that are intentionally unavailable locally.
	// Keep literal private IP blocking, but let the configured proxy resolve hostnames.
	return nil
}

func httpTransport(cfg ConfigState, allowPrivate bool, insecure bool) *http.Transport {
	if insecure {
		// Insecure requests must not share the cached proxy transport: the
		// shared instance keeps strict certificate verification for all other
		// callers. Build a dedicated transport that skips verification.
		transport := newProxyHTTPTransport(cfg, allowPrivate)
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		return transport
	}
	return proxyHTTPTransport(cfg, allowPrivate)
}

type httpDecodedReadCloser struct {
	reader  io.Reader
	closers []io.Closer
}

func (r *httpDecodedReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *httpDecodedReadCloser) Close() error {
	var firstErr error
	for i := len(r.closers) - 1; i >= 0; i-- {
		if err := r.closers[i].Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

type zstdReadCloser struct {
	*zstd.Decoder
}

func (r zstdReadCloser) Close() error {
	r.Decoder.Close()
	return nil
}

func decodedHTTPBody(resp *http.Response) (io.ReadCloser, string, error) {
	if resp == nil || resp.Body == nil {
		return io.NopCloser(bytes.NewReader(nil)), "", nil
	}
	contentEncoding := strings.TrimSpace(resp.Header.Get("Content-Encoding"))
	if contentEncoding == "" {
		return resp.Body, "", nil
	}

	reader := io.Reader(resp.Body)
	closers := []io.Closer{resp.Body}
	encodings := splitHTTPContentEncodings(contentEncoding)
	for i := len(encodings) - 1; i >= 0; i-- {
		encoding := encodings[i]
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			gr, err := gzip.NewReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode gzip response: %w", err)
			}
			reader = gr
			closers = append(closers, gr)
		case "deflate":
			dr, err := newDeflateReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode deflate response: %w", err)
			}
			reader = dr
			closers = append(closers, dr)
		case "br":
			reader = brotli.NewReader(reader)
		case "zstd", "x-zstd":
			zr, err := zstd.NewReader(reader)
			if err != nil {
				_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
				return nil, contentEncoding, fmt.Errorf("decode zstd response: %w", err)
			}
			reader = zr
			closers = append(closers, zstdReadCloser{zr})
		default:
			_ = (&httpDecodedReadCloser{reader: reader, closers: closers}).Close()
			return nil, contentEncoding, fmt.Errorf("unsupported content encoding %q", encoding)
		}
	}
	return &httpDecodedReadCloser{reader: reader, closers: closers}, contentEncoding, nil
}

func splitHTTPContentEncodings(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func newDeflateReader(r io.Reader) (io.ReadCloser, error) {
	br := bufio.NewReader(r)
	if header, err := br.Peek(2); err == nil && looksLikeZlibHeader(header) {
		return zlib.NewReader(br)
	}
	return flate.NewReader(br), nil
}

func looksLikeZlibHeader(header []byte) bool {
	if len(header) < 2 {
		return false
	}
	cmf := header[0]
	flg := header[1]
	word := int(cmf)<<8 | int(flg)
	return cmf&0x0f == 8 && word > 0 && word%31 == 0
}

func decodeHTTPText(data []byte, contentType string) string {
	if len(data) == 0 {
		return ""
	}
	// Fast path: the overwhelming majority of HTTP responses are already
	// UTF-8 (JSON APIs, modern HTML). Skip the encoding detection and
	// transform.Bytes allocation entirely for those cases.
	if utf8.Valid(data) {
		return string(data)
	}
	enc, _, _ := charset.DetermineEncoding(data, contentType)
	decoded, _, err := transform.Bytes(enc.NewDecoder(), data)
	if err == nil {
		return string(decoded)
	}
	return string(bytes.ToValidUTF8(data, []byte("\uFFFD")))
}

func isPrivateHTTPAddress(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

func normalizeHeaders(headers map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		key := http.CanonicalHeaderKey(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		out[key] = v
	}
	return out
}

func headerValue(headers map[string]string, key string) string {
	for k, v := range headers {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}

func mergeStringMaps(base, override map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

func boolPtr(v bool) *bool {
	return &v
}

func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func (a *App) waitHTTPRateLimit(ctx context.Context, target *url.URL) error {
	host := strings.ToLower(target.Hostname())
	if host == "" {
		return nil
	}
	a.httpRateMu.Lock()
	last := a.httpLastHost[host]
	wait := httpRateDelay - time.Since(last)
	if wait <= 0 {
		a.httpLastHost[host] = time.Now()
		a.httpRateMu.Unlock()
		return nil
	}
	a.httpRateMu.Unlock()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}

	a.httpRateMu.Lock()
	a.httpLastHost[host] = time.Now()
	a.httpRateMu.Unlock()
	return nil
}

func readLimited(r io.Reader, limit int) ([]byte, bool, error) {
	// Pre-grow to the limit so we don't pay for 2-3 reallocations as the
	// buffer climbs through 64K → 128K → 256K on typical HTML/JSON bodies.
	buf := bytes.NewBuffer(make([]byte, 0, limit+1))
	_, err := io.Copy(buf, io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	data := buf.Bytes()
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

func flattenHTTPHeaders(headers http.Header) map[string]string {
	out := map[string]string{}
	for k, values := range headers {
		out[k] = strings.Join(values, ", ")
	}
	return out
}

func isTextResponse(mediaType string, data []byte) bool {
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "text/") {
		return true
	}
	switch mediaType {
	case "application/json", "application/xml", "application/xhtml+xml", "application/javascript", "application/x-javascript", "image/svg+xml":
		return true
	}
	return utf8.Valid(data)
}

func isHTMLContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return contentType == "text/html" || contentType == "application/xhtml+xml" || strings.Contains(contentType, "html")
}

// htmlExtractContent parses the page with Readability (the Readability.js
// algorithm) and returns the main-content title and plain text, keeping
// paragraph breaks and pre-formatted blocks intact. Links (up to maxWebFetchLinks
// entries with anchor text) are still collected from the raw document because
// Readability does not emit them.
func htmlExtractContent(data []byte, finalURL string) (string, string, []WebFetchLink, error) {
	baseURL, _ := url.Parse(finalURL)
	article, err := readability.FromReader(bytes.NewReader(data), baseURL)
	if err != nil {
		return "", "", nil, err
	}
	var sb strings.Builder
	if err := article.RenderText(&sb); err != nil {
		return "", "", nil, err
	}
	docTitle, links := collectHTMLTitleAndLinks(data, baseURL)
	title := strings.TrimSpace(article.Title())
	if title == "" {
		title = docTitle
	}
	return title, strings.TrimSpace(sb.String()), links, nil
}

const (
	maxWebFetchLinks       = 80
	webFetchFormatReadable = "readable"
	webFetchFormatRaw      = "raw"
)

// collectHTMLTitleAndLinks walks the raw document once and returns the
// <title> text plus up to maxWebFetchLinks absolute links with anchor text.
func collectHTMLTitleAndLinks(data []byte, baseURL *url.URL) (string, []WebFetchLink) {
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", nil
	}
	titleParts := []string{}
	links := []WebFetchLink{}
	// pendingLink tracks the <a> whose link text we are currently
	// collecting. -1 means no link is pending.
	pendingLink := -1
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inTitle bool) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "title":
				inTitle = true
			case "a":
				if len(links) < maxWebFetchLinks {
					if href := htmlAttr(n, "href"); href != "" {
						if u, err := url.Parse(strings.TrimSpace(href)); err == nil {
							if baseURL != nil {
								u = baseURL.ResolveReference(u)
							}
							links = append(links, WebFetchLink{URL: u.String()})
							pendingLink = len(links) - 1
						}
					}
				}
			}
		}
		if n.Type == html.TextNode {
			if text := strings.TrimSpace(n.Data); text != "" {
				if inTitle {
					titleParts = append(titleParts, text)
				} else if pendingLink >= 0 && pendingLink < len(links) && links[pendingLink].Text == "" {
					links[pendingLink].Text = truncateRunes(normalizeWhitespace(text), 80)
					pendingLink = -1
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inTitle)
		}
	}
	walk(doc, false)
	return normalizeWhitespace(strings.Join(titleParts, " ")), links
}

func htmlAttr(n *html.Node, key string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

func normalizeWhitespace(text string) string {
	fields := strings.Fields(text)
	return strings.Join(fields, " ")
}

func truncateRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}
