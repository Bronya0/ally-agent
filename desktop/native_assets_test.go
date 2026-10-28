// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"ally-dev/resources"
)

func TestNativeAssetsSurvivePackageRelocation(t *testing.T) {
	for _, name := range []string{"chat", "settings", "github"} {
		if originalRailIcons[name] == nil {
			t.Errorf("embedded icon %q is missing", name)
		}
	}
	if poses := decodeAvatarStyle("glm"); poses["idle"] == nil {
		t.Fatal("default avatar is missing after moving the desktop package")
	}
	for _, name := range []string{"icons/LICENSE", "avatar/glm/NOTICE.txt"} {
		if data, err := resources.Assets.ReadFile(name); err != nil || len(data) == 0 {
			t.Errorf("embedded notice %q is missing: %v", name, err)
		}
	}
}
