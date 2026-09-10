// SPDX-License-Identifier: GPL-3.0-only
//
// Copyright (C) 2026 tangssst <tangssst@qq.com>
// GitHub: https://github.com/Bronya0/ally-agent
//
// This file is part of ally-agent, licensed under the GNU General
// Public License v3. See the LICENSE file for details.
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// 主窗口几何持久化：~/.ally_agent/window.json 记录 x/y/width/height/maximized，
// 启动时经 WebviewWindowOptions 恢复（创建即定位，无闪烁），退出时落盘一次。
//
// 坐标全程使用 Wails 的 DIP（逻辑像素）：Size()/Position()/options 均为 DIP，
// DPI 换算由 Wails 在创建（ScreenNearestDipRect + dipToPhysicalRect）时自动完成，
// 跨显示器缩放无需手工处理。最大化期间不更新普通 bounds（否则还原尺寸会被
// 最大化几何污染）；最小化期间 Windows 上 Position() 返回 -32000 一类占位值，
// 直接跳过。落盘只读内存快照、绝不查询窗口——ServiceShutdown 时窗口通常已销毁
// （WindowClosing 默认监听器注册在先，会先执行 markAsDestroyed）。
const (
	windowStateFilename = "window.json"
	// windowStateMaxDimension 是恢复时的宽高上限，挡住损坏/异常值；
	// 下限复用 MinWindowWidth/Height。
	windowStateMaxDimension = 16384
	// 判定窗口"可见"的最小交叉尺寸：窗口矩形与任一屏幕的可见交叉小于该值时
	// 视为离屏（保存坐标来自已断开的显示器），一次性居中修复。
	windowVisibleMinWidth  = 100
	windowVisibleMinHeight = 50
)

// windowState 是 window.json 的持久化形状。
type windowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized"`
}

func windowStatePath() string {
	return filepath.Join(appDataDir(), windowStateFilename)
}

// loadWindowState 读取并校验持久化的窗口几何。ok=false 表示没有可用状态
// （首次启动、文件缺失/损坏或数值越界），调用方应使用默认窗口参数。
func loadWindowState() (windowState, bool) {
	var s windowState
	data, err := os.ReadFile(windowStatePath())
	if err != nil {
		return s, false
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return windowState{}, false
	}
	if s.Width < MinWindowWidth || s.Height < MinWindowHeight ||
		s.Width > windowStateMaxDimension || s.Height > windowStateMaxDimension {
		return windowState{}, false
	}
	return s, true
}

// persistWindowState 原子写入窗口几何。失败返回错误；调用方按宿主状态降级，
// 不影响应用退出。
func persistWindowState(s windowState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return safeWriteFileWithDir(windowStatePath(), data, 0o600, true)
}

// ApplySavedWindowGeometry 把持久化的窗口几何应用到主窗口 options。
// main.go 在 NewWithOptions 前调用；返回是否应用了持久化状态。
//
// InitialPosition 语义（Windows PostCreate 顺序：先处理 StartState、后定位）：
//   - 普通恢复：X/Y 非零时设 WindowXY，PostCreate 的 setPosition 会重新断言
//     保存的坐标（与创建时的放置一致；两条路径都经最近屏幕换算）。
//   - 最大化恢复：只设 StartState=Maximised，不设 WindowXY——PostCreate 对
//     最大化窗口执行 setPosition（SetWindowPos 带尺寸）会破坏最大化状态，而
//     默认的 center() 走 SWP_NOSIZE 对最大化窗口近似 no-op。窗口创建本身仍
//     使用非零 X/Y/W/H，因此取消最大化后能精确回到保存的位置和尺寸。
func ApplySavedWindowGeometry(opts *application.WebviewWindowOptions) bool {
	s, ok := loadWindowState()
	if !ok {
		return false
	}
	opts.Width = s.Width
	opts.Height = s.Height
	if s.X != 0 || s.Y != 0 {
		opts.X, opts.Y = s.X, s.Y
		if !s.Maximized {
			opts.InitialPosition = application.WindowXY
		}
	}
	if s.Maximized {
		opts.StartState = application.WindowStateMaximised
	}
	return true
}

// windowStateTracker 维护窗口几何的内存快照。窗口事件回调里实时查询窗口
// （IsMaximised/Size/Position 均经 InvokeSync 到主线程，事件回调跑在独立
// goroutine，安全）；落盘只发生在 ServiceShutdown。
type windowStateTracker struct {
	mu      sync.Mutex
	state   windowState
	has     bool
	checked bool // 离屏一次性修复是否已执行
}

// update 用事件时刻的窗口实况刷新快照。最大化只更新标志位，普通 bounds 保留
// 最大化之前的值；宽高非正（销毁途中的事件）直接忽略。
func (t *windowStateTracker) update(maximized bool, x, y, w, h int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state.Maximized = maximized
	if maximized || w <= 0 || h <= 0 {
		return
	}
	// Windows 上 X=Y=0 会让创建路径走 CW_USEDEFAULT 系统级联定位，
	// 偏移 1px 规避；对用户不可见。
	if x == 0 && y == 0 {
		x, y = 1, 1
	}
	t.state.X, t.state.Y, t.state.Width, t.state.Height = x, y, w, h
	t.has = true
}

func (t *windowStateTracker) snapshot() (windowState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.has
}

// installWindowStateTracking 注册几何监听并初始化 tracker。用已保存状态播种，
// 保证"整轮未触发任何窗口事件"的会话也会写回一致值。main.go 的 SetWindow
// 在 Run 之前调用，此时注册 OnWindowEvent 是安全的（与 installWebviewZoomResync
// 同机制）。
func (a *App) installWindowStateTracking(window *application.WebviewWindow) {
	if window == nil {
		return
	}
	tracker := &windowStateTracker{}
	if s, ok := loadWindowState(); ok {
		tracker.state = s
		tracker.has = true
	}
	if a.wails == nil {
		a.wails = &wailsAppHandle{}
	}
	a.wails.windowState = tracker

	onGeometry := func(*application.WindowEvent) {
		// 最小化期间 Windows 报告占位坐标（-32000），跳过；快照保留
		// 最小化之前的普通几何。
		if window.IsMinimised() {
			return
		}
		maximized := window.IsMaximised()
		x, y, w, h := 0, 0, 0, 0
		if !maximized {
			x, y = window.Position()
			w, h = window.Size()
		}
		tracker.update(maximized, x, y, w, h)
		a.ensureWindowOnScreen(window, tracker)
	}
	window.OnWindowEvent(events.Common.WindowDidResize, onGeometry)
	window.OnWindowEvent(events.Common.WindowDidMove, onGeometry)
	window.OnWindowEvent(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
		a.ensureWindowOnScreen(window, tracker)
	})
}

// ensureWindowOnScreen 一次性校验窗口是否落在任一当前屏幕内；不在（保存坐标
// 来自已断开的显示器，Wails 创建路径只做最近屏幕的 DPI 换算、不吸附位置）
// 则居中。窗口创建时的 resize 事件与 RuntimeReady 双保险触发；屏幕缓存尚未
// 填充时不消耗一次性机会。
func (a *App) ensureWindowOnScreen(window *application.WebviewWindow, tracker *windowStateTracker) {
	if a.wails == nil || a.wails.app == nil {
		return
	}
	screens := a.wails.app.Screen.GetAll()
	if len(screens) == 0 {
		return
	}
	tracker.mu.Lock()
	if tracker.checked {
		tracker.mu.Unlock()
		return
	}
	tracker.checked = true
	tracker.mu.Unlock()

	x, y := window.Position()
	w, h := window.Size()
	if w <= 0 || h <= 0 {
		return
	}
	for _, screen := range screens {
		visible := screen.Bounds.Intersect(application.Rect{X: x, Y: y, Width: w, Height: h})
		if visible.Width >= windowVisibleMinWidth && visible.Height >= windowVisibleMinHeight {
			return
		}
	}
	window.Center()
}

// saveWindowState 在应用关闭（ServiceShutdown）时把最近的窗口几何落盘。
// 只读 tracker 快照，不查询窗口。
func (a *App) saveWindowState() {
	if a.wails == nil || a.wails.windowState == nil {
		return
	}
	s, ok := a.wails.windowState.snapshot()
	if !ok {
		return
	}
	_ = persistWindowState(s)
}
