// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// 资源卡把短的元信息压成一行标签。这条守的是两件事：端口、用户、风险级别不再各占
// 一行（同一 y），以及和标题重复的值不被画第二遍（host 通常就是别名）。
// 一处放松都会让卡片重新变高，或者多出一行重复内容。
func TestResourceCardPutsShortFieldsOnOneLine(t *testing.T) {
	a := &desktop{prefs: defaultPrefs()}
	result := map[string]any{
		"count": 1,
		"servers": []any{map[string]any{
			"alias": "db-1", "host": "db-1", "port": 5432, "username": "deploy",
			"description": "主库，只在内网访问", "riskLevel": "high", "status": "approved",
		}},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(a.theme())
		c.DisableAnimations()
		ui.Column(c).Fill().Padding(12).Children(func() {
			a.toolResourceCards(c, "k", "ssh_cluster", result, false)
		})
	}, 700, 220)

	port, okPort := tt.Find("端口 5432")
	user, okUser := tt.Find("用户 deploy")
	risk, okRisk := tt.Find("风险级别 高")
	if !okPort || !okUser || !okRisk {
		t.Fatalf("short fields missing from the card: port=%v user=%v risk=%v in %v", okPort, okUser, okRisk, tt.Texts())
	}
	if port.Y != user.Y || user.Y != risk.Y {
		t.Fatalf("port, user and risk must share one row: port.y=%v user.y=%v risk.y=%v", port.Y, user.Y, risk.Y)
	}

	// host 等于别名，只该由标题画一次。
	duplicates := 0
	for _, text := range tt.Texts() {
		if text == "db-1" {
			duplicates++
		}
	}
	if duplicates != 1 {
		t.Fatalf("a host equal to the alias was drawn %d times, want 1: %v", duplicates, tt.Texts())
	}
}
