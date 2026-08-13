// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import "testing"

// atomFeedSample is a trimmed excerpt of the real GitHub releases Atom feed
// for Bronya0/ally-agent (2026-08-04). <content> bodies are elided to keep
// the fixture compact; only the fields parseAtomLatestTag reads are kept.
const atomFeedSample = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:media="http://search.yahoo.com/mrss/" xml:lang="en-US">
  <id>tag:github.com,2008:https://github.com/Bronya0/ally-agent/releases</id>
  <link rel="alternate" type="text/html" href="https://github.com/Bronya0/ally-agent/releases"/>
  <link rel="self" type="application/atom+xml" href="https://github.com/Bronya0/ally-agent/releases.atom"/>
  <title>Release notes from ally-agent</title>
  <updated>2026-08-04T01:21:42Z</updated>
  <entry>
    <id>tag:github.com,2008:Repository/1298065682/v1.6.0</id>
    <updated>2026-08-04T02:16:11Z</updated>
    <link rel="alternate" type="text/html" href="https://github.com/Bronya0/ally-agent/releases/tag/v1.6.0"/>
    <title>v1.6.0发布</title>
    <content type="html">release notes elided</content>
  </entry>
  <entry>
    <id>tag:github.com,2008:Repository/1298065682/v1.5.0</id>
    <updated>2026-07-30T10:00:00Z</updated>
    <link rel="alternate" type="text/html" href="https://github.com/Bronya0/ally-agent/releases/tag/v1.5.0"/>
    <title>v1.5.0</title>
    <content type="html">older release</content>
  </entry>
</feed>`

func TestParseAtomLatestTag(t *testing.T) {
	tag, err := parseAtomLatestTag([]byte(atomFeedSample))
	if err != nil {
		t.Fatalf("parseAtomLatestTag returned error: %v", err)
	}
	if tag != "v1.6.0" {
		t.Fatalf("expected tag v1.6.0, got %q", tag)
	}
}

// TestParseAtomLatestTagIDFallback verifies the <id> fallback path used when
// no alternate link is present. The tag is the trailing path segment of the
// <id> element.
func TestParseAtomLatestTagIDFallback(t *testing.T) {
	const feed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>tag:github.com,2008:Repository/1298065682/v2.0.1</id>
    <title>v2.0.1</title>
  </entry>
</feed>`
	tag, err := parseAtomLatestTag([]byte(feed))
	if err != nil {
		t.Fatalf("parseAtomLatestTag returned error: %v", err)
	}
	if tag != "v2.0.1" {
		t.Fatalf("expected tag v2.0.1, got %q", tag)
	}
}

func TestParseAtomLatestTagEmptyFeed(t *testing.T) {
	const feed = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom"></feed>`
	if _, err := parseAtomLatestTag([]byte(feed)); err == nil {
		t.Fatal("expected error for empty feed, got nil")
	}
}
