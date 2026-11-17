package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// These adapters keep each native widget responsible for its own input,
// focus, accessibility and virtualized rows. Only common typography or chrome
// is added here; callers can still apply their own sizes and labels.
func Autocomplete(c *ui.Context, value *string, suggestions []string) ui.Element {
	return AutocompleteStatus(c, value, suggestions, FieldNormal)
}

// AutocompleteStatus applies the shared validation state to autocomplete.
func AutocompleteStatus(c *ui.Context, value *string, suggestions []string, status FieldStatus) ui.Element {
	return InputChromeStatus(c, ui.Autocomplete(c, value, suggestions), status).Height(ControlHeight)
}

func DecimalInput(c *ui.Context, value *string, lo, hi, step string) ui.Element {
	return ui.DecimalInput(c, value, lo, hi, step).FontSize(FontBody)
}

func DateInput(c *ui.Context, value *time.Time) ui.Element {
	return ui.DateInput(c, value).FontSize(FontBody)
}

func TimeInput(c *ui.Context, value *time.Time) ui.Element {
	return ui.TimeInput(c, value).FontSize(FontBody)
}

func TokenField(c *ui.Context, values *[]string, suggestions []string) ui.Element {
	return TokenFieldStatus(c, values, suggestions, FieldNormal)
}

// TokenFieldStatus applies the shared validation state without constraining
// the field's height as its tokens wrap.
func TokenFieldStatus(c *ui.Context, values *[]string, suggestions []string, status FieldStatus) ui.Element {
	return InputChromeStatus(c, ui.TokenField(c, values, suggestions), status)
}

func EditableText(c *ui.Context, value *string) ui.Element {
	return ui.EditableText(c, value).FontSize(FontBody)
}

func Calendar(c *ui.Context, value *time.Time) ui.Element {
	return ui.Calendar(c, value).FontSize(FontBody)
}

func ColorPicker(c *ui.Context, value *ui.Color) ui.Element {
	return ui.ColorPicker(c, value).FontSize(FontBody)
}

func ColorWell(c *ui.Context, value *ui.Color) ui.Element { return ui.ColorWell(c, value) }

func StepSlider(c *ui.Context, value *float64, lo, hi, step float64) ui.Element {
	return ui.StepSlider(c, value, lo, hi, step)
}

func RangeSlider(c *ui.Context, low, high *float64, lo, hi, step float64) ui.Element {
	return ui.RangeSlider(c, low, high, lo, hi, step)
}

func Stepper(c *ui.Context, value *float64, lo, hi, step float64) ui.Element {
	return ui.Stepper(c, value, lo, hi, step).FontSize(FontBody)
}

func Rating(c *ui.Context, value *int, maximum int) ui.Element { return ui.Rating(c, value, maximum) }

func Toggle(c *ui.Context, value *bool, label string) ui.Element {
	return ui.Toggle(c, value, label).FontSize(FontBody)
}

func ToggleGroup(c *ui.Context, build func()) ui.Element { return ui.ToggleGroup(c, build) }
func CheckboxGroup(c *ui.Context, label string, build func()) ui.Element {
	return ui.CheckboxGroup(c, label, build).FontSize(FontBody)
}
func Radio[T comparable](c *ui.Context, selected *T, value T, label string) ui.Element {
	return ui.Radio(c, selected, value, label).FontSize(FontBody)
}
func RadioGroup(c *ui.Context, build func()) ui.Element { return ui.RadioGroup(c, build) }

func SplitButton(c *ui.Context, label string) ui.SplitButtonParts {
	return ui.SplitButton(c, label)
}
func PrimarySplitButton(c *ui.Context, label string) ui.SplitButtonParts {
	return ui.PrimarySplitButton(c, label)
}

func Breadcrumbs(c *ui.Context, items []string, chosen *int) ui.Element {
	return ui.Breadcrumbs(c, items, chosen).FontSize(FontBody)
}

func BackButton(c *ui.Context, router *ui.Router) ui.Element    { return ui.BackButton(c, router) }
func ForwardButton(c *ui.Context, router *ui.Router) ui.Element { return ui.ForwardButton(c, router) }
func Toolbar(c *ui.Context, build func()) ui.Element            { return ui.Toolbar(c, build).FontSize(FontBody) }

func Sidebar(c *ui.Context, selected *string, build func()) ui.Element {
	return ui.Sidebar(c, selected, build).FontSize(FontBody)
}
func SidebarItem(c *ui.Context, id string, icon *ui.SVG, label string) ui.Element {
	return ui.SidebarItem(c, id, icon, label).FontSize(FontBody)
}
func SidebarSection(c *ui.Context, title string, open *bool, build func()) ui.Element {
	return ui.SidebarSection(c, title, open, build).FontSize(FontBody)
}

func Avatar(c *ui.Context, name string, image *ui.Bitmap) ui.Element {
	return ui.Avatar(c, name, image)
}
func Badge(c *ui.Context, text string) ui.Element { return ui.Badge(c, text).FontSize(FontTiny) }
func Meter(c *ui.Context, value, lo, hi float64, levels *ui.MeterLevels) ui.Element {
	return ui.Meter(c, value, lo, hi, levels)
}
func Spinner(c *ui.Context) ui.Element { return ui.Spinner(c) }
func PrimaryButton(c *ui.Context, label string) ui.Element {
	return Action(c, label, ActionOptions{Tone: Primary, Style: Secondary})
}
func Link(c *ui.Context, label, url string) ui.Element {
	return ui.Link(c, label, url).FontSize(FontBody)
}
func Icon(c *ui.Context, icon *ui.SVG) ui.Element          { return ui.Icon(c, icon) }
func Image(c *ui.Context, image ui.ImageSource) ui.Element { return ui.Image(c, image) }
func RichText(c *ui.Context, spans ...ui.Span) ui.Element {
	return ui.RichText(c, spans...).FontSize(FontBody)
}
func Video(c *ui.Context, player *ui.VideoPlayer) ui.Element { return ui.Video(c, player) }

// WebView only hosts a view supplied by the caller; creation and lifecycle
// remain subject to the application's preview restrictions.
func WebView(c *ui.Context, view ui.NativeWebView) ui.Element   { return ui.WebView(c, view) }
func NativeView(c *ui.Context, view ui.PlatformView) ui.Element { return ui.NativeView(c, view) }
func TextInputBuffer(c *ui.Context, value *ui.TextBuffer) ui.Element {
	return TextInputBufferStatus(c, value, FieldNormal)
}
func TextInputBufferStatus(c *ui.Context, value *ui.TextBuffer, status FieldStatus) ui.Element {
	return InputChromeStatus(c, ui.TextInputBuffer(c, value), status).Height(ControlHeight)
}
func TextAreaBuffer(c *ui.Context, value *ui.TextBuffer) ui.Element {
	return TextAreaBufferStatus(c, value, FieldNormal)
}
func TextAreaBufferStatus(c *ui.Context, value *ui.TextBuffer, status FieldStatus) ui.Element {
	return InputChromeStatus(c, ui.TextAreaBuffer(c, value), status)
}
func Modal(c *ui.Context, open *bool, build func()) ui.Element { return ui.Modal(c, open, build) }
func Popover(c *ui.Context, anchor ui.Element, open *bool, build func()) ui.Element {
	return ui.Popover(c, anchor, open, build)
}
func PopoverMenuButton(c *ui.Context, label string, build func(*ui.Menu), options ...ui.PopoverMenuButtonOptions) ui.Element {
	return ui.PopoverMenuButton(c, label, build, options...)
}
func AlertDialog(c *ui.Context, open *bool, title, message string, buttons ...string) int {
	return ui.AlertDialog(c, open, title, message, buttons...)
}

func Accordion(c *ui.Context, build func()) ui.Element {
	return ui.Accordion(c, build).FontSize(FontBody)
}
func AccordionItem(c *ui.Context, title string, open *bool, build func()) ui.Element {
	return ui.AccordionItem(c, title, open, build).FontSize(FontBody)
}
func Collapsible(c *ui.Context, label string, open *bool, build func()) ui.Element {
	return ui.Collapsible(c, label, open, build).FontSize(FontBody)
}
func Form(c *ui.Context, build func()) ui.Element { return ui.Form(c, build).FontSize(FontBody) }
func Field(c *ui.Context, label string, build func()) ui.Element {
	return ui.Field(c, label, build).FontSize(FontBody)
}
func Fieldset(c *ui.Context, legend string, build func()) ui.Element {
	return ui.Fieldset(c, legend, build).FontSize(FontBody)
}
func FindBar(c *ui.Context, open *bool, query *string, matches int, current *int) ui.Element {
	return ui.FindBar(c, open, query, matches, current).FontSize(FontBody)
}

// List and table adapters leave their virtualized data model with MyGo.
func List(c *ui.Context, state *ui.ListState, n int, row func(int)) ui.Element {
	return ui.List(c, state, n, row).FontSize(FontBody)
}
func Table(c *ui.Context, state *ui.ListState, columns []ui.TableColumn, n int, cell func(int, int)) ui.Element {
	return ui.Table(c, state, columns, n, cell).FontSize(FontBody)
}
func GridView(c *ui.Context, state *ui.GridState, n int, minWidth, height float32, item func(int)) ui.Element {
	return ui.GridView(c, state, n, minWidth, height, item).FontSize(FontBody)
}
func Tree(c *ui.Context, build func()) ui.Element { return ui.Tree(c, build).FontSize(FontBody) }
func TreeItem(c *ui.Context, label string, open *bool, children func()) ui.Element {
	return ui.TreeItem(c, label, open, children).FontSize(FontBody)
}
func Outline[K comparable](c *ui.Context, state *ui.OutlineState[K], roots []K, children func(K) []K, row func(K)) ui.Element {
	return ui.Outline(c, state, roots, children, row).FontSize(FontBody)
}
func OutlineTable[K comparable](c *ui.Context, state *ui.OutlineState[K], columns []ui.TableColumn, roots []K, children func(K) []K, cell func(K, int)) ui.Element {
	return ui.OutlineTable(c, state, columns, roots, children, cell).FontSize(FontBody)
}
