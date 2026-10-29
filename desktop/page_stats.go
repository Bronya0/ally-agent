// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

var chartPalette = []ui.Color{
	ui.Hex("#8da5df"), ui.Hex("#6fbf8f"), ui.Hex("#e0a458"), ui.Hex("#c98bd8"),
	ui.Hex("#63b3d8"), ui.Hex("#e85454"), ui.Hex("#a0b46a"), ui.Hex("#d88bb0"),
	ui.Hex("#7f9ad6"), ui.Hex("#bfa05f"),
}

func (a *desktop) loadStats(manual ...bool) {
	go func() {
		stats := a.core.GetTokenStats()
		a.win.Update(func() {
			a.stats = stats
			if len(manual) > 0 && manual[0] {
				if stats.Error != "" {
					a.notifyError(stats.Error)
				} else {
					a.notifySuccess("用量统计已刷新")
				}
			}
		})
	}()
}

func (a *desktop) statsPageBody(c *ui.Context) {
	if a.stats.Error != "" {
		notice(c, "error", a.stats.Error)
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		fieldNote(c, "今日 / 近 7 日 / 本月 三个固定区间")
		ui.Spacer(c)
		flat(c, "刷新").OnClick(func() { a.loadStats(true) })
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

// statsBars draws the fixed 30-day stacked bar chart with mygo's chart package:
// a band scale places the bars and a linear scale their heights, so the axis
// ticks, the bar geometry and the hover target come from the package instead of
// being recomputed by hand against the plot box.
func (a *desktop) statsBars(c *ui.Context) {
	daily := a.stats.Daily
	card(c, "stats-bars", func() {
		ui.Row(c).Children(func() {
			sectionTitle(c, "近 30 天用量")
			ui.Spacer(c)
			legendSwatch(c, "输入", chartPalette[0])
			legendSwatch(c, "输出", chartPalette[1])
		})
		if len(daily) == 0 {
			fieldNote(c, "近 30 天暂无数据。")
			return
		}
		dates := make([]string, len(daily))
		input := make([]float64, len(daily))
		output := make([]float64, len(daily))
		maxValue := 1.0
		for i, d := range daily {
			dates[i] = monthDay(d.Date)
			input[i] = float64(d.InputTokens)
			output[i] = float64(d.OutputTokens)
			if total := input[i] + output[i]; total > maxValue {
				maxValue = total
			}
		}
		x := chart.NewBand(dates...).Padding(0.28, 0.06)
		y := chart.NewLinear(0, maxValue).Nice(4)
		ticks := y.Ticks(4)
		theme := c.Theme()
		chart.Plot(c.Key("stats-bars-plot"), chart.Axes{Left: ticks}, func(area ui.Element) {
			area.Draw(func(p *ui.Painter, r ui.Rect) {
				for _, tick := range ticks {
					p.Line(r.X, chart.Y(r, tick.At), r.X+r.W, chart.Y(r, tick.At), 1, theme.Border)
				}
			})
			for i, d := range daily {
				tip := fmt.Sprintf("%s · 输入 %s · 输出 %s · 合计 %s", d.Date, fmtK(d.InputTokens), fmtK(d.OutputTokens), fmtK(d.InputTokens+d.OutputTokens))
				// Stacked: input from zero, output on top of it.
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(0, input[i])).Radius(2).Background(chartPalette[0]), tip)
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(input[i], input[i]+output[i])).Radius(2).Background(chartPalette[1]), tip)
			}
		}).Label("近30天用量图").Height(200).Gap(6)
		ui.Row(c).FillWidth().Children(func() {
			fieldNote(c, monthDay(daily[0].Date))
			ui.Spacer(c)
			fieldNote(c, monthDay(daily[len(daily)-1].Date))
		})
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
				uikit.Tooltip(c, cell, fmt.Sprintf("%s · %s tokens", key, fmtK(value)))
				cell.Children(func() { ui.Text(c, fmt.Sprint(day)).FontSize(fsTiny).TextColor(c.Theme().TextMuted) })
			}
		})
		_ = grid
	})
}

// statsDonut draws a ring with a legend: chart.Pie divides the circle, chart.Sector
// gives each slice's path and chart.SliceAt says which slice is under the pointer,
// so the ring is no longer painted as a fan of small quads.
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
		values := make([]float64, len(stats))
		for i, item := range stats {
			values[i] = float64(item.InputTokens + item.OutputTokens)
		}
		slices := chart.Pie(values, 0.03)
		ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
			box := ui.Box(c).Size(150, 150).Label(title + "环形图")
			hover := -1
			if px, py, over := box.PointerPosition(); over {
				bounds := box.Bounds()
				if i, ok := chart.SliceAt(slices, px-bounds.W/2, py-bounds.H/2, 47, 63); ok {
					hover = i
				}
			}
			box.Draw(func(p *ui.Painter, r ui.Rect) {
				cx, cy := r.X+r.W/2, r.Y+r.H/2
				for i, slice := range slices {
					outer := float32(63)
					if i == hover {
						outer = 67
					}
					p.FillPath(chart.Sector(cx, cy, 47, outer, slice), chartPalette[i%len(chartPalette)])
				}
				p.Text(cx-26, cy-6, fmtK(total), 14, c.Theme().Text)
				p.Text(cx-22, cy+12, "总 tokens", 9, c.Theme().TextMuted)
			})
			if hover >= 0 {
				item := stats[hover]
				name := item.Name
				if name == "" {
					name = item.FullName
				}
				uikit.Tooltip(c, box, fmt.Sprintf("%s · 输入 %s · 输出 %s · %.1f%%", name, fmtK(item.InputTokens), fmtK(item.OutputTokens), float64(item.InputTokens+item.OutputTokens)*100/float64(total)))
			}
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				for i, item := range stats {
					value := item.InputTokens + item.OutputTokens
					name := item.Name
					if name == "" {
						name = item.FullName
					}
					uikit.Tooltip(c, ui.Row(c).Gap(gapField).AlignItems(ui.Center), fmt.Sprintf("%s · 输入 %s · 输出 %s", name, fmtK(item.InputTokens), fmtK(item.OutputTokens))).Children(func() {
						ui.Box(c).Size(8, 8).Radius(2).Background(chartPalette[i%len(chartPalette)])
						uikit.Tooltip(c, ui.Text(c, short(name, 26)).FontSize(fsTiny), item.FullName).SingleLine()
						ui.Spacer(c)
						fieldNote(c, fmt.Sprintf("%.1f%%", float64(value)*100/float64(total)))
						ui.Text(c, fmtK(value)).FontSize(fsTiny).TextColor(c.Theme().TextMuted)
					})
				}
			})
		})
	})
}
