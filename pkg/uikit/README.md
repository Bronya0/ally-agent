# Native UI kit

Independent native MyGo v0.7.1 controls. Copy this directory into another Go
project and update its import path; it depends only on MyGo's `ui` package.
It contains no Ally state, filesystem access, goroutines, fonts or business logic.
Use the host's `ui.Theme` to supply typography and semantic colors.

```go
import "ally-dev/pkg/uikit"

func view(c *ui.Context) {
    uikit.Select(c.Key("auth"), &state.Auth, []string{"API Key", "OAuth"}).Label("Authentication")
    uikit.Tabs(c.Key("tabs"), &state.Tab, "General", "Advanced")
    uikit.Card(c.Key("account")).Children(func() {
        uikit.FieldLabel(c, "Name")
        uikit.TextField(c.Key("name"), &state.Name, "Example account")
    })
}
```

Exports: Button, Select, editable Combobox, Tabs, Segmented, Card, Tag,
FieldLabel, FieldNote, TextField, TextArea, DialogPanel, Popup and PopupTransition.
ControlHeight and Radius are shared sizing tokens. Choice rows use a subtle
accent fill plus a check for selection; hover and keyboard highlight use the
surface hover color. Tabs set pointer cursors on each interactive item and label.

Values and focus belong to the host. Set `c.Key` before constructing dynamic
controls. Never retain returned Elements/Parts between build passes. Attach
`Changed`/`OnClick` as with MyGo controls. Use `DialogPanel` inside DialogBase;
its transition animates entry and removal, respects reduced motion, and does
not animate layout dimensions. Popup uses the same transition and chrome.

The Ally desktop's widgets.go is the compatibility layer for old call sites.
New projects should import this package directly.

`Input`, `Area` and `InputChrome` share visible borders, hover and focus colors.
For side-by-side fields, use fractional grid tracks and stretch the cell's
Column with `JustifySelf(ui.Stretch)`; put `FillWidth` on the control inside
that cell. A percentage width directly on a grid child can resolve against
the whole grid instead of its track in MyGo v0.7.1. The regression tests cover
field gaps and the desktop's context action grid.

Prefer MyGo components before composing a replacement. Select and editable
Combobox use `SelectBase` / `ComboboxBase`; Tabs use `TabsBase`; input fields use
MyGo text inputs. This package styles their parts and preserves their native
keyboard navigation, focus, popup dismissal and value binding. Use MyGo's
Scroll / ScrollHorizontal for overflow rather than implementing wheel handling.
