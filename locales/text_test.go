// SPDX-License-Identifier: GPL-3.0-only
package locales

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/i18n"
)

var printfToken = regexp.MustCompile(`%(?:\[\d+\])?[-+# 0]*(?:\d+|\*)?(?:\.(?:\d+|\*))?(?:\[\d+\])?[a-zA-Z%]`)
var printfIndex = regexp.MustCompile(`\[(\d+)\]`)

func formatsMatch(source, translated string) bool {
	want := printfToken.FindAllString(source, -1)
	got := printfToken.FindAllString(translated, -1)
	if reflect.DeepEqual(want, got) {
		return true
	}
	arguments := []string{}
	percents := 0
	for _, token := range want {
		if token == "%%" {
			percents++
			continue
		}
		arguments = append(arguments, token)
	}
	seen := map[int]bool{}
	for _, token := range got {
		if token == "%%" {
			percents--
			continue
		}
		index := printfIndex.FindStringSubmatch(token)
		if len(index) != 2 {
			return false
		}
		n, _ := strconv.Atoi(index[1])
		if n < 1 || n > len(arguments) || seen[n] || printfIndex.ReplaceAllString(token, "") != arguments[n-1] {
			return false
		}
		seen[n] = true
	}
	return percents == 0 && len(seen) == len(arguments)
}

func TestCatalogCoverageAndFormats(t *testing.T) {
	tags := []string{"en", "zh-Hans", "ja", "ko"}
	catalogs := map[string]map[string]json.RawMessage{}
	for _, tag := range tags {
		data, err := files.ReadFile(tag + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(data, &entries); err != nil {
			t.Fatal(err)
		}
		catalogs[tag] = entries
	}
	for source, key := range sourceKeys {
		for _, tag := range tags {
			if _, ok := catalogs[tag][key]; !ok {
				t.Fatalf("%s lacks %s", tag, key)
			}
			message, resolved, ok := Catalog.Message(tag, key)
			if !ok || resolved != tag {
				t.Fatalf("%s/%s falls back to %s", tag, key, resolved)
			}
			text := message.Format(tag, nil)
			if tag == "zh-Hans" && text != source {
				t.Fatalf("literal did not round-trip: %s: %q != %q", key, text, source)
			}
			if !formatsMatch(source, text) || strings.Contains(text, "ZXQ") || strings.Contains(text, "ZXNEWLINE") {
				t.Fatalf("%s/%s changed its format arguments", tag, key)
			}
		}
	}
	for key := range catalogs["en"] {
		if key[0] == '@' {
			continue
		}
		for _, tag := range tags {
			if _, ok := catalogs[tag][key]; !ok {
				t.Fatalf("%s lacks %s", tag, key)
			}
		}
	}
}

func TestTextPreservesUnknownContent(t *testing.T) {
	for _, input := range []string{"", "user {data} %s 日本語 한국어 🐋", "https://example.com/a?q=1", "用户自己写的内容"} {
		if got := Text(input); got != input {
			t.Fatalf("unknown content changed: %q", got)
		}
	}
}

func TestTextSwitchAndEnglishFallback(t *testing.T) {
	defer i18n.SetLocaleFunc(nil)
	for _, tag := range []string{"en", "zh-Hans", "ja", "ko", "fr"} {
		i18n.SetLocaleFunc(func() string { return tag })
		key := sourceKeys["界面语言"]
		if got, want := Text("界面语言"), Catalog.Format(tag, key, nil); got != want {
			t.Fatalf("%s: %q != %q", tag, got, want)
		}
	}
	if got := Text("界面语言"); got != Catalog.Format("en", sourceKeys["界面语言"], nil) {
		t.Fatal("unknown locale did not fall back to English")
	}
}

func TestFixedMessageLookupAllocatesNothing(t *testing.T) {
	i18n.SetLocaleFunc(func() string { return "en" })
	defer i18n.SetLocaleFunc(nil)
	Text("界面语言")
	if allocations := testing.AllocsPerRun(100, func() { Text("界面语言") }); allocations != 0 {
		t.Fatalf("warm lookup allocates %v", allocations)
	}
}
