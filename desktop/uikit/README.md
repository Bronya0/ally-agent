# Native UI kit

Independent native MyGo v0.7.1 controls. Copy this directory into another Go
project and update its import path; it depends only on MyGo's `ui` package.
It contains no Ally state, filesystem access, goroutines, fonts or business logic.
Use the host's `ui.Theme` to supply typography and semantic colors.

```go
import "ally-dev/desktop/uikit"

func view(c *ui.Context) {
    uikit.Select(c.Key("auth"), &state.Auth, []string{"API Key", "OAuth"}).Label("Authentication")
    uikit.Tabs(c.Key("tabs"), &state.Tab, "General", "Advanced")
    uikit.Card(c.Key("account")).Children(func() {
        uikit.FieldLabel(c, "Name")
        uikit.TextField(c.Key("name"), &state.Name, "Example account")
    })
}
```

Exports. Form controls: Button, IconButton, Select, editable Combobox, Tabs, Segmented,
PasswordInput, NumberInput, TextField, TextArea, FieldLabel, FieldNote. Chrome: Card,
RowCard, Divider, PageHeader, SectionHeader, EmptyState, BusyRow, Notice, InlineError,
StatCard, KV, Tag, Label, Muted, SectionTitle, HoverRow, SelectableRow. Dialogs: DialogPanel,
Popup, PopupTransition.

Button takes a ButtonVariant (secondary, primary, danger, ghost) and returns its label
element too, for the few call sites whose label colour is not the button's own.
IconButton takes an IconTone and a *ui.SVG: the kit does not know an app's icon table,
so the caller resolves names to images. WarningInk and FaintInk fill the two tones
ui.Theme does not carry.

ControlHeight, Radius, the Font and Gap scales and the RadiusCard / RadiusSmall pair
are shared sizing tokens; components take padding and radius as arguments rather than
baking one page's spacing into them. Choice rows use a subtle accent fill plus a check
for selection; hover and keyboard highlight use the surface hover color. Tabs set
pointer cursors on each interactive item and label.

TabUnderline, TabInk and TabRadius are the strip look every tab row in the app
wears: a rounded box whose bottom edge carries the accent, so the line curves
away at both ends, and a label that gains weight rather than colour when chosen.
Tabs uses them, and so does the header's workspace strip, which holds a running
dot and a close button in the same chrome. Apply them to a tab of any size,
taking TabRadius as the proportion for a 30 DIP tab. When the chosen edge needs
to animate, set the border colour on top of TabUnderline, as Tabs does.

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

PasswordInput combines a native text input and a visibility button. NumberInput
combines native text and button controls: MyGo v0.7.1's NumberInput has a fixed
inner width and does not expose its parts for styling. Both controls keep their
state per keyed instance.
