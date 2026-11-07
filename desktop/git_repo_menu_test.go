package desktop

import (
	"testing"

	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

func TestGitRepoMenuRetainsBranchAndStatusContent(t *testing.T) {
	open := true
	chosen := ""
	repo := backend.GitRepoItem{Path: "repo-id", Name: "repository", Branch: "feature", Ahead: 2, Added: 3, Deleted: 1}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme((&desktop{}).theme())
		c.DisableAnimations()
		uikit.MenuButton(c.Key("repos"), &open, uikit.MenuOptions{Width: 320}, func(b ui.Element) { b.Label("repos").Size(60, 28) }, func(m ui.MenuParts) {
			c.Key(repo.Path)
			gitRepoMenuItem(c, m.Item(), repo).OnClick(func() { chosen = repo.Path })
		})
	}, 600, 400)
	for _, text := range []string{"repository", "feature", "↑2", "+3", "−1"} {
		if !tt.HasText(text) {
			t.Fatalf("repository menu lost %q: %v", text, tt.Texts())
		}
	}
	repo.Branch = "main"
	tt.Frame()
	if tt.HasText("feature") || !tt.HasText("main") {
		t.Fatal("branch changes did not reach the same repository row")
	}
	if err := tt.Click("repository/main"); err != nil {
		t.Fatal(err)
	}
	if chosen != repo.Path || open {
		t.Fatalf("repository selection failed: chosen=%q open=%v", chosen, open)
	}
}
