// SPDX-License-Identifier: GPL-3.0-only
package locales

import "github.com/egoist/mygo/i18n"

// Text resolves a known UI message. Unknown text, including user content and
// provider responses, passes through unchanged and never enters a cache.
// The generated catalog caches fixed messages; a warm lookup allocates nothing.
func Text(source string) string {
	key, ok := sourceKeys[source]
	if !ok {
		return source
	}
	text := Catalog.Format(i18n.Locale(), key, nil)
	if text == key {
		return source
	}
	return text
}
