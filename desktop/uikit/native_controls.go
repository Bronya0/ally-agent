package uikit

import "github.com/egoist/mygo/ui"

// Checkbox keeps MyGo's keyboard, mixed-state and focus behavior while using
// the kit's typography. CheckboxState remains available for tri-state groups.
func Checkbox(c *ui.Context, checked *bool, label string) ui.Element {
	return ui.Checkbox(c, checked, label).FontSize(FontBody)
}

func CheckboxState(c *ui.Context, state *ui.CheckState, label string) ui.Element {
	return ui.CheckboxState(c, state, label).FontSize(FontBody)
}

// SearchField shares the input border, focus and theme treatment while keeping
// the native search control's keyboard, clearing and submit behavior.
func SearchField(c *ui.Context, value *string) ui.Element {
	return SearchFieldStatus(c, value, FieldNormal)
}

// SearchFieldStatus adds the same validation treatment as InputStatus while
// leaving search, clear and submit behavior to MyGo.
func SearchFieldStatus(c *ui.Context, value *string, status FieldStatus) ui.Element {
	return InputChromeStatus(c, ui.SearchField(c, value), status).Height(ControlHeight)
}

// Slider and Progress use native drawing and accessibility; callers retain
// control over width, labels and disabled state.
func Slider(c *ui.Context, value *float64, lo, hi float64) ui.Element {
	return ui.Slider(c, value, lo, hi)
}

func Progress(c *ui.Context, value float64) ui.Element {
	return ui.Progress(c, value)
}
