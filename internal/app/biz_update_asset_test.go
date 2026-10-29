// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"net/url"
	"testing"
)

func TestListUpdateAssetWithoutNetworkOrConfig(t *testing.T) {
	if !updatePlatformSupported() {
		t.Skip("automatic updates are unsupported on this platform")
	}
	// A zero-value App has no config/runtime. URL resolution must stay local,
	// preserve the exact tag spelling, and leave size discovery to the download.
	for _, tag := range []string{"v1.26.0", "1.26.0"} {
		asset := new(App).ListUpdateAsset(" " + tag + " ")
		name := expectedAssetName(tag)
		want := "https://github.com/Bronya0/ally-agent/releases/download/" + tag + "/" + name
		if !asset.OK || asset.Tag != tag || asset.Name != name || asset.URL != want || asset.Size != 0 {
			t.Fatalf("unexpected asset: %#v, want URL %q and unknown size", asset, want)
		}
	}
}

func TestListUpdateAssetRejectsUnsafeAndPrereleaseTags(t *testing.T) {
	if !updatePlatformSupported() {
		t.Skip("automatic updates are unsupported on this platform")
	}
	for _, tag := range []string{"", "../v1.2.3", "v1.2.3/other", "v1.2.3?x=1", "v1.2.3#x", "v1.2.3-beta.1", "v1.2.3+build.1"} {
		asset := new(App).ListUpdateAsset(tag)
		if asset.OK || asset.Error == "" || asset.URL != "" {
			t.Fatalf("unsafe tag %q returned an asset: %#v", tag, asset)
		}
	}
	asset := new(App).ListUpdateAsset("v1.2.3")
	parsed, err := url.Parse(asset.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("unexpected download destination %q: %v", asset.URL, err)
	}
}

func TestUpdateAssetNamesMatchPublishedPlatforms(t *testing.T) {
	for _, tc := range []struct{ os, arch, suffix string }{
		{"windows", "amd64", "windows-x64.zip"}, {"windows", "arm64", "windows-arm64.zip"},
		{"darwin", "universal", "macos-universal.dmg"}, {"darwin", "amd64", "macos-x64.dmg"}, {"darwin", "arm64", "macos-arm64.dmg"},
		{"linux", "amd64", "linux-x64.tar.gz"}, {"linux", "arm64", "linux-arm64.tar.gz"},
	} {
		for _, tag := range []string{"v1.27.0", "1.27.0"} {
			if got, want := expectedAssetNameForPlatform(tag, tc.os, tc.arch), "Ally-"+tag+"-"+tc.suffix; got != want {
				t.Errorf("%s/%s: %s != %s", tc.os, tc.arch, got, want)
			}
		}
	}
}
