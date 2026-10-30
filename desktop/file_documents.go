package desktop

import (
	"ally-dev/desktop/uikit"
	backend "ally-dev/internal/app"
	"ally-dev/internal/documentpreview"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
)

func propertiesOnlyFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pdf", ".doc", ".xls", ".ppt", ".pptx", ".zip", ".7z", ".rar", ".gz", ".tar", ".exe", ".dll", ".bin", ".mp4", ".mov", ".mp3", ".wav":
		return true
	}
	return false
}

func (a *desktop) cancelFilePreview() {
	if a.filePreviewCancel != nil {
		a.filePreviewCancel()
		a.filePreviewCancel = nil
	}
	a.filePreviewRoot = ""
}

func (a *desktop) openSystemFile(path string) {
	if t := a.current(); t != nil {
		req := backend.WorkspacePathRequest{Workspace: t.Workspace, Path: path}
		a.job(func() error { return a.core.OpenWorkspaceFile(req) })
	}
}

func (a *desktop) documentFileView(c *ui.Context, file *workspaceFile) {
	doc := file.Document
	ui.Column(c.Key("document-" + file.Path)).Grow(1).MinHeight(0).FillWidth().Gap(gapField).Padding(padRow).Children(func() {
		if doc.Truncated {
			fieldNote(c, "已显示部分内容，可用系统应用查看完整文档。", a.secondaryInk())
		}
		if strings.EqualFold(filepath.Ext(file.Path), ".xlsx") && len(doc.Sheets) == 0 {
			emptyState(c, "暂无可预览的工作表", "可使用系统应用查看完整文档。", a.secondaryInk())
			return
		}
		if len(doc.Sheets) > 0 {
			ui.Row(c).FillWidth().Gap(gapField).AlignItems(ui.Center).Children(func() {
				fieldLabel(c, "工作表")
				names := make([]string, len(doc.Sheets))
				for i, s := range doc.Sheets {
					names[i] = s.Name
				}
				uikit.Select(c.Key("document-sheet"), &file.DocumentSheet, names).Width(220).Label("预览工作表").OnChange(func() { file.DocumentList = ui.ListState{} })
			})
			fieldNote(c, "内容预览 · 公式显示已保存的结果，不含图表与复杂格式", a.secondaryInk())
			var sheet *documentpreview.Sheet
			for i := range doc.Sheets {
				if doc.Sheets[i].Name == file.DocumentSheet {
					sheet = &doc.Sheets[i]
					break
				}
			}
			if sheet == nil {
				return
			}
			cols := []ui.TableColumn{{Title: "行", Width: 54, Fixed: true, RowHeader: true}}
			for i := 0; i < sheet.Columns; i++ {
				cols = append(cols, ui.TableColumn{Title: documentpreview.ColumnName(i), Width: 140, MinWidth: 70})
			}
			ui.Table(c.Key("document-table-"+file.DocumentSheet), &file.DocumentList, cols, len(sheet.Rows), func(row, col int) {
				value := fmt.Sprint(sheet.Rows[row].Number)
				if col > 0 {
					value = ""
					if col <= len(sheet.Rows[row].Values) {
						value = sheet.Rows[row].Values[col-1]
					}
				}
				uikit.Tooltip(c, ui.Text(c, value).FontSize(fsSmall).SingleLine().FillWidth().MinWidth(0).Selectable(), value)
			}).Fill().MinHeight(0).Label("文档表格预览")
			return
		}
		fieldNote(c, "正文预览 · 不含图片、页眉页脚和原版分页", a.secondaryInk())
		if len(doc.Paragraphs) == 0 {
			emptyState(c, "暂无可预览的正文", "可使用系统应用查看完整文档。", a.secondaryInk())
			return
		}
		ui.List(c.Key("document-paragraphs"), &file.DocumentList, len(doc.Paragraphs), func(index int) {
			p := doc.Paragraphs[index]
			ui.Column(c).FillWidth().Padding(6, 10).Children(func() {
				text := ui.Text(c, p.Text).FillWidth().FontSize(fsBody).LineHeight(1.6).Selectable()
				if p.Heading > 0 {
					text.Bold().FontSize(max(fsBody, 22-float32(p.Heading)*2))
				}
			})
		}).Fill().MinHeight(0).Label("文档正文预览")
	})
}
