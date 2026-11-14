// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestReadOnlyTailCacheSeparatesRequestedLengths(t *testing.T) {
	var source strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&source, "log line %d\n", i)
	}
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%v", remote), func(t *testing.T) {
			cache := newSessionReadCache()
			data := source.String()
			readTail := func(lines int) BatchReadResultItem {
				t.Helper()
				var item BatchReadResultItem
				if remote {
					var err error
					item, err = buildRemoteReadResultItem(remoteRawFile{Path: "big.log", Data: []byte(data), Size: maxReadFileBytes + 1, ReadOnlyTail: true}, BatchReadFileRequest{Path: "big.log", TailLines: lines})
					if err != nil {
						t.Fatal(err)
					}
				} else {
					item = batchReadResultItem(ReadFileRequest{Path: "big.log", StartLine: -lines}, buildReadOnlyTailResult("big.log", []byte(data), maxReadFileBytes+1, lines))
				}
				result := &BatchReadResult{Files: []BatchReadResultItem{item}}
				cache.dedupeResult(result, func(item *BatchReadResultItem) string { return item.Path })
				return result.Files[0]
			}
			for i, lines := range []int{100, 500, 100, 500} {
				item := readTail(lines)
				if item.Reused != (i >= 2) || (item.Content == "") != item.Reused {
					t.Fatalf("tail %d at step %d lost its cached payload: %#v", lines, i, item)
				}
				if item.StartLine != 0 || item.EndLine != 0 || item.Editable || item.Version != "" || item.RangeStatus != "read_only_tail" {
					t.Fatalf("tail acquired edit/absolute-line metadata: %#v", item)
				}
			}
			data += "changed tail\n"
			if item := readTail(100); item.Reused || !strings.Contains(item.Content, "changed tail") {
				t.Fatalf("changed tail was omitted: %#v", item)
			}
		})
	}
}
