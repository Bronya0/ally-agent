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
			a.statsChart = buildStatsBars(stats.Daily)
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
			statsTokens(c, summary.TotalTokens).FontSize(fsDisplay).Bold()
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

// statsBarsData is the 30-day chart's derived series. The page rebuilds the
// widget tree every frame; dates/tips/slices change only when the snapshot does.
type statsBarsData struct {
	dates  []string
	input  []float64
	output []float64
	tips   []string
	max    float64
}

func buildStatsBars(daily []backend.TokenDailyStat) statsBarsData {
	data := statsBarsData{
		dates:  make([]string, len(daily)),
		input:  make([]float64, len(daily)),
		output: make([]float64, len(daily)),
		tips:   make([]string, len(daily)),
		max:    1.0,
	}
	for i, d := range daily {
		data.dates[i] = monthDay(d.Date)
		data.input[i] = float64(d.InputTokens)
		data.output[i] = float64(d.OutputTokens)
		if total := data.input[i] + data.output[i]; total > data.max {
			data.max = total
		}
		data.tips[i] = fmt.Sprintf("%s · 输入 %s · 输出 %s · 合计 %s tokens", d.Date, statsExact(d.InputTokens), statsExact(d.OutputTokens), statsExact(d.InputTokens+d.OutputTokens))
	}
	return data
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
		bars := a.statsChart
		if len(bars.dates) != len(daily) {
			bars = buildStatsBars(daily)
		}
		dates := bars.dates
		input := bars.input
		output := bars.output
		maxValue := bars.max
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
			for i := range daily {
				tip := bars.tips[i]
				// Stacked: input from zero, output on top of it.
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(0, input[i])).Radius(2).Background(chartPalette[0]), tip)
				uikit.Tooltip(c, chart.Rect(c, x.Band(i), y.Span(input[i], input[i]+output[i])).Radius(2).Background(chartPalette[1]), tip)
			}
		}).Label("近30天用量图").Height(200).Gap(gapInline)
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

// heatmapDay is one day of the month grid: the text and the tooltip are
// formatted once per month instead of once per frame.
type heatmapDay struct {
	label   string
	tooltip string
	ratio   float64
	value   int
}

// heatmapKey is the month plus a fingerprint of the day totals behind it.
type heatmapKey struct {
	month string
	sum   uint64
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
		now := time.Now()
		// The month and the day totals are the whole input of the cells. Both go
		// into the key, so a new month, a new day or new numbers rebuild them.
		key := heatmapKey{month: now.Format("2006-01"), sum: 14695981039346656037}
		for _, d := range daily {
			for i := 0; i < len(d.Date); i++ {
				key.sum = (key.sum ^ uint64(d.Date[i])) * 1099511628211
			}
			key.sum = (key.sum ^ uint64(d.InputTokens)) * 1099511628211
			key.sum = (key.sum ^ uint64(d.OutputTokens)) * 1099511628211
		}
		first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		daysInMonth := time.Date(now.Year(), now.Month()+1, 0, 0, 0, 0, 0, time.Local).Day()
		offset := (int(first.Weekday()) + 6) % 7 // Monday-first
		cells := offset + daysInMonth
		rows := (cells + 6) / 7
		grid := ui.Grid(c.Key("heatmap-grid")).Columns(7).Gap(gapTight)
		days := memoAt[memoOf[heatmapKey, []heatmapDay]](grid, "days").get(key, func(heatmapKey) []heatmapDay {
			byDay := make(map[string]int, len(daily))
			maxValue := 1
			for _, d := range daily {
				total := d.InputTokens + d.OutputTokens
				byDay[d.Date] = total
				if total > maxValue {
					maxValue = total
				}
			}
			monthDays := make([]heatmapDay, daysInMonth)
			for day := 1; day <= daysInMonth; day++ {
				date := time.Date(now.Year(), now.Month(), day, 0, 0, 0, 0, time.Local)
				dateKey := date.Format("2006-01-02")
				value := byDay[dateKey]
				monthDays[day-1] = heatmapDay{
					label:   fmt.Sprint(day),
					tooltip: fmt.Sprintf("%s · %s tokens", dateKey, statsExact(value)),
					ratio:   float64(value) / float64(maxValue),
					value:   value,
				}
			}
			return monthDays
		})
		grid.Children(func() {
			for i := 0; i < rows*7; i++ {
				day := i - offset + 1
				if day < 1 || day > daysInMonth {
					ui.Box(c).Height(26)
					continue
				}
				data := days[day-1]
				fill := c.Theme().SurfaceHover
				if data.value > 0 {
					fill = fill.Mix(c.Theme().Success, float32(0.15+0.45*data.ratio))
				}
				cell := ui.Box(c).Height(26).Radius(4).Background(fill).Center()
				uikit.Tooltip(c, cell, data.tooltip)
				cell.Children(func() { ui.Text(c, data.label).FontSize(fsTiny).TextColor(c.Theme().Text) })
			}
		})
		fieldNote(c, "色彩越明显，用量越高", a.secondaryInk())
	}).Basis(400).Grow(1).MinWidth(0)
}

type statsDonutRow struct {
	name, legendTip, hoverTip, percent, value string
}

// The input is checked field by field by the heading's local memo. Derived
// labels and shares belong only to this card, including after same-size edits.
type statsDonutData struct {
	total                int
	totalLabel, totalTip string
	slices               []chart.Slice
	rows                 []statsDonutRow
}

func buildStatsDonut(stats []backend.TokenDimensionStat) statsDonutData {
	var data statsDonutData
	for _, item := range stats {
		data.total += item.InputTokens + item.OutputTokens
	}
	if data.total == 0 || len(stats) == 0 {
		return data
	}
	data.totalLabel = statsNumber(float64(data.total))
	data.totalTip = statsExact(data.total) + " tokens"
	values := make([]float64, len(stats))
	data.rows = make([]statsDonutRow, len(stats))
	for i, item := range stats {
		value := item.InputTokens + item.OutputTokens
		values[i] = float64(value)
		name := item.Name
		if name == "" {
			name = item.FullName
		}
		fullName := item.FullName
		if fullName == "" {
			fullName = name
		}
		percent := fmt.Sprintf("%.1f%%", float64(value)*100/float64(data.total))
		data.rows[i] = statsDonutRow{
			name:      name,
			legendTip: fmt.Sprintf("%s · 输入 %s · 输出 %s tokens", fullName, statsExact(item.InputTokens), statsExact(item.OutputTokens)),
			hoverTip:  fmt.Sprintf("%s · 输入 %s · 输出 %s · %s", name, statsExact(item.InputTokens), statsExact(item.OutputTokens), percent),
			percent:   percent,
			value:     statsNumber(float64(value)),
		}
	}
	data.slices = chart.Pie(values, 0.03)
	return data
}

type statsDonutSectorKey struct {
	cx, cy, outer float32
	slice         chart.Slice
}

// A sector changes with its live center, share or hover radius. Keep one path
// per slice so repainting does not rebuild its curves, without fixing geometry
// to an earlier layout or retaining paths for past snapshots.
type statsDonutSectorMemo = memoOf[statsDonutSectorKey, *ui.Path]

func statsDonutSector(key statsDonutSectorKey) *ui.Path {
	return chart.Sector(key.cx, key.cy, 47, key.outer, key.slice)
}

// statsDonut draws a ring with a legend: chart.Pie divides the circle, chart.Sector
// gives each slice's path and chart.SliceAt says which slice is under the pointer,
// so the ring is no longer painted as a fan of small quads.
func (a *desktop) statsDonut(c *ui.Context, title string, stats []backend.TokenDimensionStat) {
	card(c, "donut-"+title, func() {
		heading := sectionTitle(c.Key("donut-heading"), title)
		data := memoAt[indexedMemo[backend.TokenDimensionStat, statsDonutData]](heading, "donut-data").get(len(stats), "",
			func(i int) backend.TokenDimensionStat { return stats[i] },
			func() statsDonutData { return buildStatsDonut(stats) })
		if data.total == 0 || len(stats) == 0 {
			fieldNote(c, "该区间暂无数据。", a.secondaryInk())
			return
		}
		slices := data.slices
		ui.Row(c).Gap(gapSection).AlignItems(ui.Center).Children(func() {
			box := ui.Box(c).Size(142, 142).Shrink(0).Center().Label(title + "环形图")
			sectors := memoAt[[]statsDonutSectorMemo](box, "sectors")
			if len(*sectors) != len(slices) {
				*sectors = make([]statsDonutSectorMemo, len(slices))
			}
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
					path := (*sectors)[i].get(statsDonutSectorKey{cx, cy, outer, slice}, statsDonutSector)
					p.FillPath(path, chartPalette[i%len(chartPalette)])
				}
			})
			box.Children(func() {
				ui.Column(c).Center().Gap(2).PassThrough().Children(func() {
					ui.Text(c, data.totalLabel).FontSize(fsHeading).Bold()
					fieldNote(c, "tokens", a.secondaryInk())
				})
			})
			if hover >= 0 {
				uikit.Tooltip(c, box, data.rows[hover].hoverTip)
			} else {
				uikit.Tooltip(c, box, data.totalTip)
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(gapInline).Children(func() {
				for i, item := range data.rows {
					uikit.Tooltip(c, ui.Row(c.Key(widgetKey(title, "stats-legend", i))).FillWidth().MinWidth(0).Gap(gapField).AlignItems(ui.Center), item.legendTip).Children(func() {
						ui.Box(c).Size(8, 8).Shrink(0).Radius(2).Background(chartPalette[i%len(chartPalette)])
						ui.Text(c, item.name).FontSize(fsTiny).SingleLine().Grow(1).MinWidth(0)
						ui.Text(c, item.percent).FontSize(fsTiny).TextColor(a.secondaryInk()).Width(46).Shrink(0).TextAlign(ui.End).NoWrap()
						ui.Text(c, item.value).Width(58).Shrink(0).TextAlign(ui.End).SingleLine().FontSize(fsTiny).TextColor(a.secondaryInk())
					})
				}
			})
		})
	}).Basis(400).Grow(1).MinWidth(0)
}
