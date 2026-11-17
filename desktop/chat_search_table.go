// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type chatSearchTableCell struct {
	index   int
	text    string
	spans   [][2]int
	sources []int
	runs    []chatSearchMarkdownRun
	base    int
}

// chatSearchTable replaces only a table that contains a searchable hit. Cells
// remain native Markdown text runs, while the surrounding table follows the
// Markdown theme's own sizing, borders, alignment and stripe rules. Returning
// false leaves tables with unsupported cell content to the regular renderer.
func (a *desktop) chatSearchTable(c *ui.Context, t *conversation, message int, doc *parser.Document, index int) bool {
	table := doc.Block(index)
	if table == nil || table.Kind != parser.Table || len(table.Align) == 0 {
		return false
	}
	s := &t.uiState().Search
	tableHasHit := false
	for _, hit := range s.cache {
		if hit.Message == message && hit.Start < table.End && hit.End > table.Start {
			tableHasHit = true
			break
		}
	}
	if !tableHasHit {
		return false
	}
	rows := make([][]chatSearchTableCell, 0, 4)
	header := make([]bool, 0, 4)
	widths := make([]float32, len(table.Align))
	matched := false
	options := t.MD[message]
	if options == nil || options.Theme == nil {
		return false
	}
	th := options.Theme
	fontSize := th.FontSize
	if fontSize <= 0 {
		fontSize, _ = readingSizes(a.config.MessageFontSize, a.config.CodeFontSize)
	}
	padX, padY := th.TableCellPadding[0]*fontSize, th.TableCellPadding[1]*fontSize
	maxWidth := th.TableMaxColumn * fontSize
	if maxWidth <= 0 {
		maxWidth = 24 * fontSize
	}
	for rowIndex := index + 1; rowIndex < table.Next; rowIndex = doc.Block(rowIndex).Next {
		row := doc.Block(rowIndex)
		if row.Kind != parser.TableRow {
			return false
		}
		cells := make([]chatSearchTableCell, 0, len(table.Align))
		column := 0
		for cellIndex := rowIndex + 1; cellIndex < row.Next; cellIndex = doc.Block(cellIndex).Next {
			if column >= len(table.Align) {
				break
			}
			cellBlock := doc.Block(cellIndex)
			if cellBlock.Kind != parser.TableCell {
				return false
			}
			projection, ok := chatSearchBlockProjectionFor(doc, cellIndex, message, s)
			if !ok {
				return false
			}
			cell := chatSearchTableCell{index: cellIndex, text: projection.text, runs: projection.runs}
			if cell.text != "" {
				cell.spans, cell.sources = chatSearchMarkdownBlockSpans(projection, message, s.cache)
				if len(cell.spans) > 0 {
					cell.base = cell.sources[0] - cell.spans[0][0]
					matched = true
				}
				w, _ := c.MeasureText(0, ui.Span{Text: cell.text})
				widths[column] = max(widths[column], min(w, maxWidth)+2*padX+1)
			} else {
				widths[column] = max(widths[column], 2*padX+1)
			}
			cells = append(cells, cell)
			column++
		}
		rows = append(rows, cells)
		header = append(header, row.Header)
	}
	if !matched {
		return false
	}
	for i := range widths {
		if widths[i] == 0 {
			widths[i] = min(maxWidth, 2*padX+1)
		}
	}
	outer := ui.ScrollHorizontal(c).AlignSelf(ui.Stretch)
	outer.Children(func() {
		grid := ui.Column(c).Border(1, th.TableBorder).Radius(fontSize * 0.3).Clip().Shrink(0).Role(ui.RoleTable)
		grid.Children(func() {
			for rowIndex, cells := range rows {
				row := ui.Row(c).Role(ui.RoleRow).AlignItems(ui.Stretch)
				if header[rowIndex] {
					row.Background(th.TableHeader)
				} else if rowIndex%2 == 1 && th.TableStripe.A > 0 {
					row.Background(th.TableStripe)
				}
				if rowIndex > 0 {
					row.BorderWidth(1, 0, 0, 0).BorderColor(th.TableBorder)
				}
				row.Children(func() {
					for column, cell := range cells {
						role, weight := ui.RoleCell, 0
						if header[rowIndex] {
							role, weight = ui.RoleColumnHeader, th.StrongWeight
						}
						box := ui.Column(c).Width(widths[column]).Padding(padY, padX).Role(role)
						if column > 0 {
							box.BorderWidth(0, 0, 0, 1).BorderColor(th.TableBorder)
						}
						align := ui.Start
						switch table.Align[column] {
						case parser.AlignCenter:
							align = ui.Center
						case parser.AlignRight:
							align = ui.End
						}
						box.Children(func() {
							if cell.text == "" {
								ui.Text(c, "").TextAlign(align)
								return
							}
							a.chatSearchMarkWeight(c, t, message, doc.Block(cell.index), cell.text, cell.spans, cell.sources, cell.runs, cell.base, weight)
						})
					}
				})
			}
		})
	})
	return true
}
