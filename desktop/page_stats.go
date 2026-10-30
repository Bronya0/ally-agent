// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"ally-dev/desktop/uikit"
	"fmt"
	"math"
	"time"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/chart"
)

var chartPalette = []ui.Color{
	ui.Hex("#8da5df"), ui.Hex("#6fbf8f"), ui.Hex("#e0a458"), ui.Hex("#c98bd8"),
	ui.Hex("#63b3d8"), ui.Hex("#e85454"), ui.Hex("#a0b46a"), ui.Hex("#d88bb0"),
	ui.Hex("#7f9ad6"), ui.Hex("#bfa05f"),
}

// MyGo handles decimal rounding/grouping; only the token-specific unit choice
// lives here. Promote before rounding so a boundary never reads "1000k".
func statsNumber(value float64) string {
	units := [...]string{"", "k", "M", "B", "T"}
	i := 0
	for math.Abs(value) >= 999.95 && i < len(units)-1 {
		value /= 1000
		i++
	}
	return i18n.FormatNumber("en", value, i18n.NumberOptions{NoGrouping: true, MaxFractionDigits: 1}) + units[i]
}

func statsExact(value int) string {
	return i18n.FormatNumber("en", value, i18n.NumberOptions{Integer: true})
}

func statsTokens(c *ui.Context, value int) ui.Element {
	return uikit.Tooltip(c, ui.Text(c, statsNumber(float64(value))).SingleLine(), statsExact(value)+" tokens")
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
		notice(c, "error", a.stats.Error, a.secondaryInk())
	}
	ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "k 千 / M 百万 / B 十亿 / T 万亿 · 悬停查看精确值").FontSize(fsSmall).TextColor(a.secondaryInk()).MinWidth(0).Shrink(1)
		ui.Spacer(c)
		flat(c, "刷新").OnClick(func() { a.loadStats(true) })
	})
	empty := a.stats.SummaryMonth.TotalTokens == 0 && a.stats.Summary7Days.TotalTokens == 0 && a.stats.SummaryToday.TotalTokens == 0
	if empty {
		emptyState(c, "暂无用量数据", "开始对话后，这里会显示 token 用量与缓存命中率。", a.secondaryInk())
		return
	}
	ui.Row(c.Key("stats-overview")).FillWidth().Wrap().AlignItems(ui.Stretch).Gap(gapSection).Children(func() {
		a.statsSummaryCard(c, "今日", a.stats.SummaryToday)
		a.statsSummaryCard(c, "近 7 日", a.stats.Summary7Days)
		a.statsSummaryCard(c, "本月", a.stats.SummaryMonth)
	})
	ui.Row(c.Key("stats-daily-panels")).FillWidth().Wrap().AlignItems(ui.Stretch).Gap(gapSection).Children(func() {
		a.statsBars(c)
		a.statsHeatmap(c)
	})
	ui.Row(c.Key("stats-donuts")).FillWidth().Wrap().AlignItems(ui.Stretch).Gap(gapSection).Children(func() {
		a.statsDonut(c, "模型 · 近 7 日", a.stats.ModelWeek)
		a.statsDonut(c, "模型 · 本月", a.stats.ModelMonth)
		a.statsDonut(c, "工作区 · 近 7 日", a.stats.WorkspaceWeek)
		a.statsDonut(c, "工作区 · 本月", a.stats.WorkspaceMonth)
	})
}

func (a *desktop) statsSummaryCard(c *ui.Context, title string, summary backend.TokenStatsSummary) {
	card(c, "stats-summary-"+title, func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(fsSmall).TextColor(a.secondaryInk())
			ui.Spacer(c)
			uikit.Tooltip(c, ui.Text(c, statsNumber(float64(summary.Requests))+" 次请求").FontSize(fsSmall).TextColor(a.secondaryInk()).NoWrap(), statsExact(summary.Requests)+" 次请求")
		})
		ui.Row(c).Gap(gapField).AlignItems(ui.Baseline).Children(func() {
			statsTokens(c, summary.TotalTokens).FontSize(28).Bold()
			fieldNote(c, "tokens", a.secondaryInk())
		})
		ui.Row(c).FillWidth().Gap(gapSection).Wrap().Children(func() {
			ui.Row(c).Gap(gapTight).Children(func() {
				fieldNote(c, "输入", a.secondaryInk())
				statsTokens(c, summary.InputTokens).FontSize(fsSmall)
			})
			ui.Row(c).Gap(gapTight).Children(func() {
				fieldNote(c, "输出", a.secondaryInk())
				statsTokens(c, summary.OutputTokens).FontSize(fsSmall)
			})
		})
		ui.Row(c).Gap(gapTight).Children(func() {
			fieldNote(c, "单次平均", a.secondaryInk())
			statsTokens(c, summary.AvgPerRequest).FontSize(fsSmall).TextColor(a.secondaryInk())
		})
		ui.Row(c).FillWidth().Children(func() {
			fieldNote(c, "缓存命中", a.secondaryInk())
			ui.Spacer(c)
			fieldNote(c, fmt.Sprintf("%.1f%%", summary.CacheHitRate*100), a.secondaryInk())
		})
		uikit.Tooltip(c, ui.Meter(c.Key("stats-cache-"+title), summary.CacheHitRate, 0, 1, nil).FillWidth().Height(4).Label(title+"缓存命中率"), "命中 "+statsExact(summary.CacheHitTokens)+" / 未命中 "+statsExact(summary.CacheMissTokens)+" tokens")
		if summary.CacheWriteTokens > 0 {
			ui.Row(c).Gap(gapTight).Children(func() {
				fieldNote(c, "缓存写入", a.secondaryInk())
				statsTokens(c, summary.CacheWriteTokens).FontSize(fsSmall).TextColor(a.secondaryInk())
			})
		}
	}).Basis(240).Grow(1).MinWidth(0).Label(title + "用量概览")
}

// statsBars draws the fixed 30-day stacked bar chart with mygo's chart package:
// a band scale places the bars and a linear scale their heights, so the axis
// ticks, the bar geometry and the hover target come from the package instead of
// being recomputed by hand against the plot box.
func (a *desktop) statsBars(c *ui.Context) {
	daily := a.stats.Daily
	card(c, "stats-bars", func() {
		ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
			sectionTitle(c, "近 30 天用量")
			ui.Spacer(c)
			legendSwatch(c, "输入", chartPalette[0])
			legendSwatch(c, "输出", chartPalette[1])
		})
		if len(daily) == 0 {
			fieldNote(c, "近 30 天暂无数据。", a.secondaryInk())
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
		y.Format = statsNumber
		ticks := y.Ticks(4)
		theme := c.Theme()
		chart.Plot(c.Key("stats-bars-plot"), chart.Axes{Left: ticks, Label: func(_ chart.Side, tick chart.Tick) {
			ui.Text(c, tick.Label).FontSize(fsTiny).TextColor(a.secondaryInk()).NoWrap()
		}}, func(area ui.Element) {
			area.Draw(func(p *ui.Painter, r ui.Rect) {
				for _, tick := range ticks {
					p.Line(r.X, chart.Y(r, tick.At), r.X+r.W, chart.Y(r, tick.At), 1, theme.Border)
				}
			})
			for i, d := range daily {
				tip := fmt.Sprintf("%s · 输入 %s · 输出 %s · 合计 %s tokens", d.Date, statsExact(d.InputTokens), statsExact(d.OutputTokens), statsExact(d.InputTokens+d.OutputTokens))
				// Stacked: input from zero, output on top of it.
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(0, input[i])).Radius(2).Background(chartPalette[0]), tip)
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(input[i], input[i]+output[i])).Radius(2).Background(chartPalette[1]), tip)
			}
		}).Label("近30天用量图").Height(200).Gap(6)
		ui.Row(c).FillWidth().Children(func() {
			fieldNote(c, monthDay(daily[0].Date), a.secondaryInk())
			ui.Spacer(c)
			fieldNote(c, monthDay(daily[len(daily)-1].Date), a.secondaryInk())
		})
	}).Basis(400).Grow(1).MinWidth(0)
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
		ui.Grid(c).Columns(7).Gap(gapTight).Children(func() {
			for _, day := range []string{"一", "二", "三", "四", "五", "六", "日"} {
				ui.Text(c, day).FontSize(fsTiny).TextAlign(ui.Center).TextColor(a.secondaryInk())
			}
		})
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
		ui.Grid(c.Key("heatmap-grid")).Columns(7).Gap(gapTight).Children(func() {
			for i := 0; i < rows*7; i++ {
				day := i - offset + 1
				if day < 1 || day > daysInMonth {
					ui.Box(c).Height(26)
					continue
				}
				date := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.Local)
				key := date.Format("2006-01-02")
				value := byDay[key]
				ratio := float64(value) / float64(maxValue)
				fill := c.Theme().SurfaceHover
				if value > 0 {
					fill = fill.Mix(c.Theme().Success, float32(0.15+0.45*ratio))
				}
				cell := ui.Box(c).Height(26).Radius(4).Background(fill).Center()
				uikit.Tooltip(c, cell, fmt.Sprintf("%s · %s tokens", key, statsExact(value)))
				cell.Children(func() { ui.Text(c, fmt.Sprint(day)).FontSize(fsTiny).TextColor(c.Theme().Text) })
			}
		})
		fieldNote(c, "色彩越明显，用量越高", a.secondaryInk())
	}).Basis(400).Grow(1).MinWidth(0)
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
			fieldNote(c, "该区间暂无数据。", a.secondaryInk())
			return
		}
		values := make([]float64, len(stats))
		for i, item := range stats {
			values[i] = float64(item.InputTokens + item.OutputTokens)
		}
		slices := chart.Pie(values, 0.03)
		ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
			box := ui.Box(c).Size(142, 142).Shrink(0).Center().Label(title + "环形图")
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
			})
			box.Children(func() {
				ui.Column(c).Center().Gap(2).PassThrough().Children(func() {
					ui.Text(c, statsNumber(float64(total))).FontSize(18).Bold()
					fieldNote(c, "tokens", a.secondaryInk())
				})
			})
			if hover >= 0 {
				item := stats[hover]
				name := item.Name
				if name == "" {
					name = item.FullName
				}
				uikit.Tooltip(c, box, fmt.Sprintf("%s · 输入 %s · 输出 %s · %.1f%%", name, statsExact(item.InputTokens), statsExact(item.OutputTokens), float64(item.InputTokens+item.OutputTokens)*100/float64(total)))
			} else {
				uikit.Tooltip(c, box, statsExact(total)+" tokens")
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
				for i, item := range stats {
					value := item.InputTokens + item.OutputTokens
					name := item.Name
					if name == "" {
						name = item.FullName
					}
					fullName := item.FullName
					if fullName == "" {
						fullName = name
					}
					uikit.Tooltip(c, ui.Row(c.Key(fmt.Sprintf("stats-legend-%s-%d", title, i))).FillWidth().MinWidth(0).Gap(gapField).AlignItems(ui.Center), fmt.Sprintf("%s · 输入 %s · 输出 %s tokens", fullName, statsExact(item.InputTokens), statsExact(item.OutputTokens))).Children(func() {
						ui.Box(c).Size(8, 8).Shrink(0).Radius(2).Background(chartPalette[i%len(chartPalette)])
						ui.Text(c, name).FontSize(fsTiny).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, fmt.Sprintf("%.1f%%", float64(value)*100/float64(total))).FontSize(fsTiny).TextColor(a.secondaryInk()).Width(46).Shrink(0).TextAlign(ui.End).NoWrap()
						ui.Text(c, statsNumber(float64(value))).Width(58).Shrink(0).TextAlign(ui.End).SingleLine().FontSize(fsTiny).TextColor(a.secondaryInk())
					})
				}
			})
		})
	}).Basis(400).Grow(1).MinWidth(0)
}
