// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"fmt"
	"math"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

var chartPalette = []ui.Color{
	ui.Hex("#8da5df"), ui.Hex("#6fbf8f"), ui.Hex("#e0a458"), ui.Hex("#c98bd8"),
	ui.Hex("#63b3d8"), ui.Hex("#e85454"), ui.Hex("#a0b46a"), ui.Hex("#d88bb0"),
	ui.Hex("#7f9ad6"), ui.Hex("#bfa05f"),
}

func (a *desktop) loadStats() {
	go func() {
		stats := a.core.GetTokenStats()
		a.win.Update(func() { a.stats = stats })
	}()
}

func (a *desktop) statsPageBody(c *ui.Context) {
	if a.stats.Error != "" {
		notice(c, "error", a.stats.Error)
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		fieldNote(c, "今日 / 近 7 日 / 本月 三个固定区间")
		ui.Spacer(c)
		flat(c, "刷新").OnClick(func() { a.loadStats() })
	})
	empty := a.stats.SummaryMonth.TotalTokens == 0 && a.stats.Summary7Days.TotalTokens == 0 && a.stats.SummaryToday.TotalTokens == 0
	if empty {
		emptyState(c, "暂无用量数据", "开始对话后，这里会显示 token 用量与缓存命中率。")
		return
	}
	ui.Grid(c.Key("stats-overview")).Columns(3).Gap(gapSection).Children(func() {
		a.statsSummaryCard(c, "今日", a.stats.SummaryToday)
		a.statsSummaryCard(c, "近 7 日", a.stats.Summary7Days)
		a.statsSummaryCard(c, "本月", a.stats.SummaryMonth)
	})
	ui.Grid(c.Key("stats-daily-panels")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
		a.statsBars(c)
		a.statsHeatmap(c)
	})
	ui.Grid(c.Key("stats-donuts")).ColumnTracks(ui.Fr(1), ui.Fr(1)).FillWidth().Gap(gapSection).Children(func() {
		a.statsDonut(c, "模型 · 近 7 日", a.stats.ModelWeek)
		a.statsDonut(c, "模型 · 本月", a.stats.ModelMonth)
		a.statsDonut(c, "工作区 · 近 7 日", a.stats.WorkspaceWeek)
		a.statsDonut(c, "工作区 · 本月", a.stats.WorkspaceMonth)
	})
}

func (a *desktop) statsSummaryCard(c *ui.Context, title string, summary backend.TokenStatsSummary) {
	card(c, "stats-summary-"+title, func() {
		ui.Row(c).Children(func() {
			ui.Text(c, title).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
			ui.Spacer(c)
			fieldNote(c, fmt.Sprintf("%d 次请求", summary.Requests))
		})
		ui.Text(c, fmtK(summary.TotalTokens)).FontSize(24).Bold()
		ui.Text(c, fmt.Sprintf("输入 %s · 输出 %s", fmtK(summary.InputTokens), fmtK(summary.OutputTokens))).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
		ui.Row(c).Gap(gapField).Children(func() {
			fieldNote(c, fmt.Sprintf("单次平均 %s", fmtK(summary.AvgPerRequest)))
			ui.Spacer(c)
			fieldNote(c, fmt.Sprintf("缓存命中 %.1f%%", summary.CacheHitRate*100))
		})
		if summary.CacheWriteTokens > 0 {
			fieldNote(c, fmt.Sprintf("缓存写入 %s", fmtK(summary.CacheWriteTokens)))
		}
	})
}

// statsBars draws the fixed 30-day stacked bar chart with local painting.
func (a *desktop) statsBars(c *ui.Context) {
	daily := a.stats.Daily
	card(c, "stats-bars", func() {
		ui.Row(c).Children(func() {
			sectionTitle(c, "近 30 天用量")
			ui.Spacer(c)
			legendSwatch(c, "输入", chartPalette[0])
			legendSwatch(c, "输出", chartPalette[1])
		})
		box := ui.Box(c).Key("stats-bars-canvas").Label("近30天用量图").Tooltip("将鼠标移到柱形上查看用量").FillWidth().Height(200)
		box.Draw(func(p *ui.Painter, r ui.Rect) {
			if len(daily) == 0 {
				return
			}
			maxValue := 1
			for _, d := range daily {
				if d.InputTokens+d.OutputTokens > maxValue {
					maxValue = d.InputTokens + d.OutputTokens
				}
			}
			left, right, top, bottom := r.X+44, r.X+r.W-8, r.Y+8, r.Y+r.H-22
			plotH := bottom - top
			for i := 0; i < 4; i++ {
				y := top + plotH*float32(i)/3
				p.Line(left, y, right, y, 1, c.Theme().Border)
				value := int(math.Round(float64(maxValue) * float64(3-i) / 3))
				p.Text(r.X+2, y-6, fmtK(value), 9, c.Theme().TextMuted)
			}
			barW := (right - left) / float32(len(daily)+1)
			for i, d := range daily {
				x := left + barW*float32(i) + barW*0.15
				inH := plotH * float32(d.InputTokens) / float32(maxValue)
				outH := plotH * float32(d.OutputTokens) / float32(maxValue)
				p.Fill(ui.Rect{X: x, Y: bottom - inH, W: barW * 0.7, H: inH}, chartPalette[0], 2)
				p.Fill(ui.Rect{X: x, Y: bottom - inH - outH, W: barW * 0.7, H: outH}, chartPalette[1], 2)
			}
			if len(daily) > 0 {
				p.Text(left, bottom+4, monthDay(daily[0].Date), 9, c.Theme().TextMuted)
				last := len(daily) - 1
				p.Text(right-40, bottom+4, monthDay(daily[last].Date), 9, c.Theme().TextMuted)
			}
		})
		if x, _, inside := box.PointerPosition(); inside && len(daily) > 0 {
			bounds := box.Bounds()
			plotWidth := bounds.W - 52
			if plotWidth > 0 && x >= 44 && x < bounds.W-8 {
				index := int((x - 44) / (plotWidth / float32(len(daily)+1)))
				if index >= 0 && index < len(daily) {
					d := daily[index]
					box.Tooltip(fmt.Sprintf("%s · 输入 %s · 输出 %s · 合计 %s", d.Date, fmtK(d.InputTokens), fmtK(d.OutputTokens), fmtK(d.InputTokens+d.OutputTokens)))
				}
			}
		}
	})
}

func monthDay(date string) string {
	if len(date) >= 10 {
		return date[5:10]
	}
	return date
}

func legendSwatch(c *ui.Context, label string, color ui.Color) {
	ui.Row(c).Gap(gapTight).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Size(8, 8).Radius(2).Background(color)
		fieldNote(c, label)
	})
}

// statsHeatmap draws the current month as a 7-column weekday grid.
func (a *desktop) statsHeatmap(c *ui.Context) {
	card(c, "stats-heatmap", func() {
		sectionTitle(c, "当月热力图")
		daily := a.stats.MonthDaily
		maxValue := 1
		byDay := map[string]int{}
		for _, d := range daily {
			total := d.InputTokens + d.OutputTokens
			byDay[d.Date] = total
			if total > maxValue {
				maxValue = total
			}
		}
		now := time.Now()
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
		offset := (int(first.Weekday()) + 6) % 7 // Monday-first
		cells := offset + daysInMonth
		rows := (cells + 6) / 7
		grid := ui.Grid(c.Key("heatmap-grid")).Columns(7).Gap(gapTight).Children(func() {
			for i := 0; i < rows*7; i++ {
				day := i - offset + 1
				if day < 1 || day > daysInMonth {
					ui.Box(c).Height(26)
					continue
				}
				date := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.Local)
				key := date.Format("2006-01-02")
				value, ok := byDay[key]
				ratio := float64(value) / float64(maxValue)
				fill := c.Theme().SurfaceHover
				switch {
				case ok && ratio >= 0.75:
					fill = ui.Hex("#4e8f68")
				case ok && ratio >= 0.5:
					fill = ui.Hex("#3f7556")
				case ok && ratio >= 0.25:
					fill = ui.Hex("#335d45")
				case ok && value > 0:
					fill = ui.Hex("#2a4636")
				}
				cell := ui.Box(c).Height(26).Radius(4).Background(fill).Center()
				cell.Tooltip(fmt.Sprintf("%s · %s tokens", key, fmtK(value)))
				cell.Children(func() { ui.Text(c, fmt.Sprint(day)).FontSize(fsTiny).TextColor(c.Theme().TextMuted) })
			}
		})
		_ = grid
	})
}

// statsDonut draws a ring with a legend; values are input+output per entry.
func (a *desktop) statsDonut(c *ui.Context, title string, stats []backend.TokenDimensionStat) {
	card(c, "donut-"+title, func() {
		sectionTitle(c, title)
		total := 0
		for _, s := range stats {
			total += s.InputTokens + s.OutputTokens
		}
		if total == 0 || len(stats) == 0 {
			fieldNote(c, "该区间暂无数据。")
			return
		}
		ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
			box := ui.Box(c).Size(150, 150).Tooltip("将鼠标移到环形图上查看用量")
			box.Draw(func(p *ui.Painter, r ui.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				radius := r.W/2 - 12
				start := float32(-90)
				for i, item := range stats {
					value := item.InputTokens + item.OutputTokens
					sweep := 360 * float32(value) / float32(total)
					color := chartPalette[i%len(chartPalette)]
					drawArc(p, cx, cy, radius, start, start+sweep, 16, color)
					start += sweep
				}
				p.Text(cx-26, cy-6, fmtK(total), 14, c.Theme().Text)
				p.Text(cx-22, cy+12, "总 tokens", 9, c.Theme().TextMuted)
			})
			if x, y, inside := box.PointerPosition(); inside {
				bounds := box.Bounds()
				dx, dy := x-bounds.W/2, y-bounds.H/2
				distance := math.Hypot(float64(dx), float64(dy))
				if distance >= float64(bounds.W/2-28) && distance <= float64(bounds.W/2-10) {
					angle := math.Atan2(float64(dy), float64(dx))*180/math.Pi + 90
					if angle < 0 {
						angle += 360
					}
					at := 0.0
					for _, item := range stats {
						value := item.InputTokens + item.OutputTokens
						at += float64(value) * 360 / float64(total)
						if angle <= at {
							name := item.Name
							if name == "" {
								name = item.FullName
							}
							box.Tooltip(fmt.Sprintf("%s · 输入 %s · 输出 %s · %.1f%%", name, fmtK(item.InputTokens), fmtK(item.OutputTokens), float64(value)*100/float64(total)))
							break
						}
					}
				}
			}
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				for i, item := range stats {
					i, item := i, item
					value := item.InputTokens + item.OutputTokens
					name := item.Name
					if name == "" {
						name = item.FullName
					}
					ui.Row(c).Gap(gapField).AlignItems(ui.Center).Tooltip(fmt.Sprintf("%s · 输入 %s · 输出 %s", name, fmtK(item.InputTokens), fmtK(item.OutputTokens))).Children(func() {
						ui.Box(c).Size(8, 8).Radius(2).Background(chartPalette[i%len(chartPalette)])
						ui.Text(c, short(name, 26)).FontSize(fsTiny).Tooltip(item.FullName).SingleLine()
						ui.Spacer(c)
						fieldNote(c, fmt.Sprintf("%.1f%%", float64(value)*100/float64(total)))
						ui.Text(c, fmtK(value)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
					})
				}
			})
		})
	})
}

// drawArc paints a ring segment with a polyline; the painter has no arc fill,
// and short segments read the same as a filled sector at this thickness.
func drawArc(p *ui.Painter, cx, cy, radius, from, to, width float32, color ui.Color) {
	steps := int(math.Abs(float64(to-from))/3) + 2
	inner := radius - width
	outer := radius
	for i := 0; i < steps; i++ {
		a1 := (from + (to-from)*float32(i)/float32(steps)) * math.Pi / 180
		a2 := (from + (to-from)*float32(i+1)/float32(steps)) * math.Pi / 180
		path := &ui.Path{}
		path.MoveTo(cx+outer*float32(math.Cos(float64(a1))), cy+outer*float32(math.Sin(float64(a1))))
		path.LineTo(cx+outer*float32(math.Cos(float64(a2))), cy+outer*float32(math.Sin(float64(a2))))
		path.LineTo(cx+inner*float32(math.Cos(float64(a2))), cy+inner*float32(math.Sin(float64(a2))))
		path.LineTo(cx+inner*float32(math.Cos(float64(a1))), cy+inner*float32(math.Sin(float64(a1))))
		path.Close()
		p.FillPath(path, color)
	}
}
