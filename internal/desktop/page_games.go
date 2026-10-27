// SPDX-License-Identifier: GPL-3.0-only
package desktop

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	backend "ally-dev/internal/app"
	"github.com/egoist/mygo/ui"
)

// ---- Game rules ----------------------------------------------------------
//
// The engine is the only arbiter: the model's reply is parsed and applied
// through these functions, and any illegal reply is fed back for a retry.

const (
	gameGomoku = iota
	gameGo
	gameXiangqi
	gameDoudizhu
)

var gameMeta = []struct {
	label string
	size  int
}{
	{"五子棋", 15}, {"围棋", 9}, {"象棋", 9}, {"斗地主", 3},
}

const gomokuSize = 15
const goSize = 9

const (
	ddzPhaseDeal = "deal"
	ddzPhaseBid  = "bid"
	ddzPhasePlay = "play"
)

func (a *desktop) resetGame(kind int) {
	g := &a.gameF
	*g = gameUI{Kind: kind, ModelA: g.ModelA, ModelB: g.ModelB, EffortA: g.EffortA, EffortB: g.EffortB, Winner: -1}
	switch kind {
	case gameGomoku:
		g.Board = make([]int, gomokuSize*gomokuSize)
		g.LastIndex = -1
		g.Status = "你先手（黑）"
	case gameGo:
		g.Board = make([]int, goSize*goSize)
		g.LastIndex = -1
		g.Ko = -1
		g.Status = "你先手（黑）"
	case gameXiangqi:
		g.XQ = xiangqiStart()
		g.LastIndex = -1
		g.Status = "你先手（红）"
	case gameDoudizhu:
		g.Phase = ddzPhaseDeal
		g.Landlord = -1
		g.Status = "发牌"
	}
}

func (a *desktop) gamesPageBody(c *ui.Context) {
	g := &a.gameF
	ui.Row(c).Gap(gapSection).AlignItems(ui.Start).Children(func() {
		a.gameSidebar(c)
		ui.Column(c).Grow(1).MinWidth(0).Gap(gapField).Children(func() {
			if !g.Started {
				card(c, "game-idle", func() {
					ui.Column(c).Gap(gapField).Padding(12).Children(func() {
						sectionTitle(c, gameMeta[g.Kind].label)
						fieldNote(c, "选好模型与棋牌后点左侧「开始对局」。棋盘文字会发给模型，模型只回一个坐标。")
						fieldNote(c, "战绩统计只在本次打开期间累计。")
					})
				})
				notice(c, "info", g.Status)
				return
			}
			switch g.Kind {
			case gameGomoku, gameGo:
				a.gameGridBoard(c)
			case gameXiangqi:
				a.gameXiangqiBoard(c)
			case gameDoudizhu:
				a.gameDoudizhu(c)
			}
			ui.Row(c).Gap(gapField).AlignItems(ui.Center).Children(func() {
				tone := "info"
				if g.Thinking {
					tone = "warn"
				}
				if g.Winner >= 0 {
					tone = "ok"
				}
				notice(c, tone, g.Status)
			})
		})
	})
}

func (a *desktop) gameSidebar(c *ui.Context) {
	g := &a.gameF
	ui.Column(c).Width(260).Gap(gapSection).Children(func() {
		card(c, "game-model", func() {
			sectionTitle(c, "执子模型")
			models := []string{}
			for _, m := range a.config.Models {
				models = append(models, modelOption(m))
			}
			if len(models) == 0 {
				fieldNote(c, "尚未配置模型。")
			}
			ui.Select(c.Key("game-model-a"), &g.ModelA, models).Label("模型 A").FillWidth().Disabled(g.Thinking)
			ui.Select(c.Key("game-effort-a"), &g.EffortA, []string{"auto", "low", "medium", "high", "xhigh", "max"}).Label("思考强度").FillWidth().Disabled(g.Thinking)
			flat(c, map[bool]string{true: "改回与模型对弈", false: "切换双模型对弈（AI vs AI）"}[g.VsAI]).Disabled(g.Thinking).OnClick(func() {
				g.VsAI = !g.VsAI
				g.Started = false
			})
			if g.VsAI {
				ui.Select(c.Key("game-model-b"), &g.ModelB, models).Label("模型 B").FillWidth().Disabled(g.Thinking)
				ui.Select(c.Key("game-effort-b"), &g.EffortB, []string{"auto", "low", "medium", "high", "xhigh", "max"}).Label("模型 B 思考强度").FillWidth().Disabled(g.Thinking)
			}
			if g.Calls > 0 {
				fieldNote(c, fmt.Sprintf("上轮 输入 %s · 输出 %s", fmtK(g.InTokens), fmtK(g.OutTokens)))
				fieldNote(c, fmt.Sprintf("本局 %d 次调用 · 累计 %s tokens", g.Calls, fmtK(g.InTokens+g.OutTokens)))
			}
			label := "开始对局"
			if g.Started {
				label = "重新开始"
			}
			if g.Thinking {
				label = "思考中…"
			}
			primary(c, label).Disabled(len(a.config.Models) == 0 || g.Thinking || g.ModelA == "").OnClick(func() { a.startGame() })
			if g.Thinking {
				dangerButton(c, "停止").OnClick(func() {
					a.core.GameAIActionCancel()
					g.Thinking = false
					g.Status = "已停止"
				})
			}
		})
		card(c, "game-pick", func() {
			sectionTitle(c, "棋牌")
			for i, meta := range gameMeta {
				i, meta := i, meta
				btn := flat(c.Key(fmt.Sprintf("game-kind-%d", i)), meta.label)
				if g.Kind == i {
					btn.TextColor(c.Theme().Accent).Background(c.Theme().SurfaceHover)
				}
				btn.Disabled(g.Thinking).OnClick(func() {
					a.resetGame(i)
					g.Started = false
				})
			}
			players := 2
			if g.Kind == gameDoudizhu {
				players = 3
			}
			if g.VsAI {
				fieldNote(c, fmt.Sprintf("两个模型轮流执子对弈，你观战（%d 人局）。", players))
			} else {
				fieldNote(c, fmt.Sprintf("%d 人局 · 你执先手，其余座位由模型接管。", players))
			}
			if g.Kind == gameGo {
				flat(c, "停一手").Disabled(!g.Started || g.Thinking || g.Winner >= 0).OnClick(func() { a.gamePass() })
			}
			if g.Kind == gameDoudizhu && g.Phase == ddzPhaseDeal {
				flat(c, "发牌").Disabled(g.Thinking).OnClick(func() { a.ddzDeal() })
			}
		})
	})
}

func (a *desktop) startGame() {
	g := &a.gameF
	a.resetGame(g.Kind)
	g.Started = true
	g.Winner = -1
	if g.VsAI {
		g.Status = "模型 A 思考中…"
		a.gameAIMove()
		return
	}
	if g.Kind == gameDoudizhu {
		a.ddzDeal()
	}
}

// ---- Board UI ------------------------------------------------------------

func (a *desktop) gameGridBoard(c *ui.Context) {
	g := &a.gameF
	size := gomokuSize
	if g.Kind == gameGo {
		size = goSize
	}
	cell := float32(28)
	if size == gomokuSize {
		cell = 24
	}
	ui.Row(c).Children(func() {
		ui.Grid(c.Key("board-grid")).Label("棋盘").Columns(size).Gap(1).Children(func() {
			for y := 0; y < size; y++ {
				for x := 0; x < size; x++ {
					index := y*size + x
					value := 0
					if index < len(g.Board) {
						value = g.Board[index]
					}
					box := ui.Box(c.Key(fmt.Sprintf("cell-%d", index))).Size(cell, cell).Radius(cell/2).
						Background(ui.Hex("#2a2723")).Border(1, ui.Hex("#3a352d"))
					if value == 1 {
						box.Background(ui.Hex("#1a1a1c")).Border(1, ui.Hex("#5a5a60"))
					} else if value == 2 {
						box.Background(ui.Hex("#e8e6e1")).Border(1, ui.Hex("#b9b6ae"))
					}
					if index == g.LastIndex {
						box.Border(2, c.Theme().Accent)
					}
					if g.Winner < 0 && !g.Thinking && g.Started && value == 0 && g.Turn == 0 {
						box.Cursor(ui.CursorPointer)
					}
					if box.Clicked() {
						a.gameHumanPlace(x, y)
					}
				}
			}
		})
	})
}

func (a *desktop) gameXiangqiBoard(c *ui.Context) {
	g := &a.gameF
	ui.Column(c).Gap(1).Children(func() {
		for y := 0; y < 10; y++ {
			ui.Row(c).Gap(1).AlignItems(ui.Center).Children(func() {
				for x := 0; x < 9; x++ {
					x, y := x, y
					index := y*9 + x
					piece := g.XQ[y][x]
					box := ui.Box(c.Key(fmt.Sprintf("xq-%d", index))).Size(38, 38).Radius(4).
						Background(ui.Hex("#d9b98a")).Border(1, ui.Hex("#a8865a")).Center()
					if index == g.Selected {
						box.Border(2, c.Theme().Accent)
					}
					box.Children(func() {
						if piece == "" {
							return
						}
						color := ui.Hex("#1d1b18")
						if strings.HasPrefix(piece, "r") {
							color = ui.Hex("#8f2f2a")
						}
						ui.Text(c, xiangqiGlyph(piece)).FontSize(18).Bold().TextColor(color)
					})
					if box.Clicked() {
						a.gameXiangqiClick(x, y)
					}
				}
			})
			if y == 4 {
				ui.Row(c).Height(14).Center().Children(func() { fieldNote(c, "楚河　　汉界") })
			}
		}
	})
}

func xiangqiGlyph(piece string) string {
	if piece == "" {
		return ""
	}
	red := strings.HasPrefix(piece, "r")
	glyphs := map[byte]string{
		'R': "车", 'N': "马", 'B': map[bool]string{true: "相", false: "象"}[red],
		'A': map[bool]string{true: "仕", false: "士"}[red], 'K': map[bool]string{true: "帅", false: "将"}[red],
		'C': "炮", 'P': map[bool]string{true: "兵", false: "卒"}[red],
	}
	return glyphs[piece[1]]
}

func (a *desktop) gameDoudizhu(c *ui.Context) {
	g := &a.gameF
	if g.Phase == ddzPhaseDeal {
		emptyState(c, "点击“发牌”开始一局", "")
		return
	}
	ui.Column(c).Gap(gapSection).Children(func() {
		ui.Row(c).Gap(gapSection).Wrap().Children(func() {
			if g.Landlord >= 0 {
				accentChip(c, fmt.Sprintf("地主：座位 %d", g.Landlord+1))
				neutralChip(c, fmt.Sprintf("底牌：%s", cardList(g.Bottom)))
			}
			for seat := 0; seat < 3; seat++ {
				label := fmt.Sprintf("座位 %d", seat+1)
				if seat == 0 {
					label += "（你）"
				}
				count := len(g.Hands[seat])
				ui.Text(c, fmt.Sprintf("%s · %d 张", label, count)).FontSize(fsSmall).TextColor(c.Theme().TextMuted)
			}
		})
		if g.Phase == ddzPhaseBid {
			ui.Row(c).Gap(gapField).Children(func() {
				primary(c, "叫地主").Disabled(g.Turn != 0 || g.Thinking).OnClick(func() { a.ddzBid(0, true) })
				flat(c, "不叫").Disabled(g.Turn != 0 || g.Thinking).OnClick(func() { a.ddzBid(0, false) })
			})
		}
		if g.Phase == ddzPhasePlay {
			hand := g.Hands[0]
			ui.Row(c).Gap(2).Wrap().Children(func() {
				for i, card := range hand {
					card := card
					selected := containsInt(g.SelectedCards, card)
					btn := ui.Box(c.Key(fmt.Sprintf("ddz-card-%d-%d", i, card))).Size(34, 46).Radius(4).
						Background(ui.Hex("#f2efe8")).Border(1, ui.Hex("#b9b2a5")).Center()
					if selected {
						btn.Border(2, c.Theme().Accent)
					}
					btn.Children(func() {
						ui.Text(c, cardName(card)).FontSize(15).Bold().TextColor(ui.Hex("#2a2620"))
					})
					if btn.Clicked() && g.Turn == 0 && !g.Thinking {
						a.ddzToggleCard(card)
					}
				}
			})
			ui.Row(c).Gap(gapField).Children(func() {
				primary(c, "出牌").Disabled(g.Turn != 0 || g.Thinking || len(g.SelectedCards) == 0).OnClick(func() { a.ddzPlay(0, g.SelectedCards) })
				flat(c, "不要").Disabled(g.Turn != 0 || g.Thinking || len(g.LastCards) == 0).OnClick(func() { a.ddzPass(0) })
				flat(c, "清空选择").OnClick(func() { g.SelectedCards = nil })
			})
			if len(g.LastCards) > 0 {
				fieldNote(c, fmt.Sprintf("上一手（座位 %d）：%s", g.LastPlayer+1, cardList(g.LastCards)))
			}
		}
	})
}

func intListString(items []int) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprint(item))
	}
	return strings.Join(parts, ",")
}

var _ = intListString

func (a *desktop) ddzToggleCard(card int) {
	g := &a.gameF
	for i, item := range g.SelectedCards {
		if item == card {
			g.SelectedCards = append(g.SelectedCards[:i], g.SelectedCards[i+1:]...)
			return
		}
	}
	g.SelectedCards = append(g.SelectedCards, card)
}

func containsInt(items []int, v int) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}

func cardName(value int) string {
	names := map[int]string{11: "J", 12: "Q", 13: "K", 14: "A", 15: "2", 16: "小王", 17: "大王"}
	if name, ok := names[value]; ok {
		return name
	}
	return fmt.Sprint(value)
}

func cardList(cards []int) string {
	sorted := append([]int(nil), cards...)
	sort.Ints(sorted)
	parts := make([]string, 0, len(sorted))
	for _, card := range sorted {
		parts = append(parts, cardName(card))
	}
	return strings.Join(parts, " ")
}

func (a *desktop) gameHumanPlace(x, y int) {
	g := &a.gameF
	if g.Winner >= 0 || g.Thinking || !g.Started || g.Turn != 0 || g.VsAI {
		return
	}
	if err := a.applyGameAction(0, map[string]any{"type": "place", "x": float64(x), "y": float64(y)}); err != nil {
		g.Status = err.Error()
		return
	}
	a.afterGameMove()
}

func (a *desktop) gamePass() {
	g := &a.gameF
	if g.Kind != gameGo || g.Thinking || !g.Started {
		return
	}
	if err := a.applyGameAction(0, map[string]any{"type": "pass"}); err != nil {
		g.Status = err.Error()
		return
	}
	a.afterGameMove()
}

func (a *desktop) afterGameMove() {
	g := &a.gameF
	if g.Winner >= 0 {
		a.gameFinished()
		return
	}
	if g.Kind == gameDoudizhu && g.Phase != ddzPhasePlay {
		return
	}
	if g.Turn != 0 || g.VsAI {
		a.gameAIMove()
		return
	}
	g.Status = "轮到你"
}

func (a *desktop) gameFinished() {
	g := &a.gameF
	switch {
	case g.Winner < 0:
		g.Status = "和棋"
	case g.VsAI:
		g.Status = fmt.Sprintf("座位 %d（%s）获胜", g.Winner+1, map[bool]string{true: "模型 B", false: "模型 A"}[g.Winner%2 == 1])
	case g.Winner == 0:
		g.Status = "你赢了 🎉"
	default:
		g.Status = "模型赢了"
	}
	g.Started = false
}

func (a *desktop) gameXiangqiClick(x, y int) {
	g := &a.gameF
	if g.Winner >= 0 || g.Thinking || !g.Started || g.VsAI || g.Turn != 0 {
		return
	}
	index := y*9 + x
	if g.Selected >= 0 && g.Selected != index {
		fromX, fromY := g.Selected%9, g.Selected/9
		if err := a.applyGameAction(0, map[string]any{"type": "move", "fromX": float64(fromX), "fromY": float64(fromY), "toX": float64(x), "toY": float64(y)}); err == nil {
			g.Selected = -1
			a.afterGameMove()
			return
		}
	}
	if g.XQ[y][x] != "" && strings.HasPrefix(g.XQ[y][x], "r") {
		g.Selected = index
		return
	}
	g.Selected = -1
}

// ---- AI turn -------------------------------------------------------------

func (a *desktop) gameAIMove() {
	g := &a.gameF
	if g.Winner >= 0 || g.Thinking {
		return
	}
	if !g.VsAI && g.Turn == 0 {
		return
	}
	seat := g.Turn
	model, effort := g.ModelA, g.EffortA
	if g.VsAI && seat%2 == 1 {
		model, effort = g.ModelB, g.EffortB
	}
	cfg, ok := a.gameModelConfig(model)
	if !ok {
		g.Status = "请选择可用模型"
		return
	}
	cfg.ReasoningEffort = effort
	g.Thinking = true
	g.Status = fmt.Sprintf("座位 %d 思考中…", seat+1)
	system, user := a.gamePrompt(seat)
	a.gameAIRequest(seat, cfg, system, user, 0)
}

// gameAIRequest asks the model for one action and applies it on the UI thread.
// Failed parses and illegal moves are fed back up to three times; the rules
// engine stays the only arbiter.
func (a *desktop) gameAIRequest(seat int, cfg backend.ModelConfig, system, user string, round int) {
	g := &a.gameF
	if round >= 3 {
		g.Thinking = false
		g.Status = "模型没有给出合法走法"
		return
	}
	go func() {
		result, err := a.core.GameAIAction(cfg, system, user)
		a.win.Update(func() {
			if err != nil {
				a.gameAIRequest(seat, cfg, system, "", round+1)
				return
			}
			action, perr := parseFirstJSON(result.Reply)
			if perr != nil {
				a.gameAIRequest(seat, cfg, "只输出一个 JSON 对象，不要包含其它文字。", user, round+1)
				return
			}
			if g.Kind == gameDoudizhu && g.Phase == ddzPhaseBid {
				value := 0
				if str(action, "type") == "bid" && int(num(action, "value")) == 1 {
					value = 1
				}
				g.Calls++
				g.InTokens += result.PromptTokens
				g.OutTokens += result.CompletionTokens
				g.Thinking = false
				a.ddzBid(seat, value == 1)
				return
			}
			if err := a.applyGameAction(seat, action); err != nil {
				a.gameAIRequest(seat, cfg, "上一次走法不合法："+err.Error()+"。请重新给出合法走法，只输出一个 JSON 对象。", user, round+1)
				return
			}
			g.Calls++
			g.InTokens += result.PromptTokens
			g.OutTokens += result.CompletionTokens
			g.Thinking = false
			if g.Winner >= 0 {
				a.gameFinished()
				return
			}
			if g.Kind == gameDoudizhu {
				a.ddzAIIfNeeded()
				return
			}
			if !g.VsAI && g.Turn == 0 {
				g.Status = "轮到你"
				return
			}
			a.gameAIMove()
		})
	}()
}

func (a *desktop) gameModelConfig(label string) (backend.ModelConfig, bool) {
	for _, m := range a.config.Models {
		if modelOption(m) == label {
			return m, true
		}
	}
	return backend.ModelConfig{}, false
}

func parseFirstJSON(reply string) (map[string]any, error) {
	start := strings.Index(reply, "{")
	if start < 0 {
		return nil, fmt.Errorf("没有 JSON")
	}
	depth := 0
	for i := start; i < len(reply); i++ {
		switch reply[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var out map[string]any
				if err := json.Unmarshal([]byte(reply[start:i+1]), &out); err != nil {
					return nil, err
				}
				return out, nil
			}
		}
	}
	return nil, fmt.Errorf("JSON 未闭合")
}

// ---- Rules ---------------------------------------------------------------

func (a *desktop) applyGameAction(player int, action map[string]any) error {
	g := &a.gameF
	if g.Winner >= 0 {
		return fmt.Errorf("对局已经结束")
	}
	switch g.Kind {
	case gameGomoku:
		return g.applyGomoku(player, action)
	case gameGo:
		return g.applyGo(player, action)
	case gameXiangqi:
		return g.applyXiangqi(player, action)
	case gameDoudizhu:
		return g.applyDoudizhu(player, action)
	}
	return fmt.Errorf("未知棋种")
}

func (g *gameUI) applyGomoku(player int, action map[string]any) error {
	if player != g.Turn || str(action, "type") != "place" {
		return fmt.Errorf("还没轮到该座位")
	}
	x, y := int(num(action, "x")), int(num(action, "y"))
	if x < 0 || y < 0 || x >= gomokuSize || y >= gomokuSize {
		return fmt.Errorf("落子位置无效")
	}
	index := y*gomokuSize + x
	if g.Board[index] != 0 {
		return fmt.Errorf("该位置已有棋子")
	}
	g.Board[index] = player + 1
	g.Turn = 1 - player
	g.LastIndex = index
	if hasFive(g.Board, x, y, player+1) {
		g.Winner = player
	}
	return nil
}

func hasFive(board []int, x, y, value int) bool {
	for _, dir := range [][2]int{{1, 0}, {0, 1}, {1, 1}, {1, -1}} {
		count := 1
		for _, sign := range []int{-1, 1} {
			for n := 1; n < gomokuSize; n++ {
				xx, yy := x+dir[0]*n*sign, y+dir[1]*n*sign
				if xx < 0 || yy < 0 || xx >= gomokuSize || yy >= gomokuSize || board[yy*gomokuSize+xx] != value {
					break
				}
				count++
			}
		}
		if count >= 5 {
			return true
		}
	}
	return false
}

func goNeighbors(index int) []int {
	x, y := index%goSize, index/goSize
	var out []int
	if x > 0 {
		out = append(out, index-1)
	}
	if x < goSize-1 {
		out = append(out, index+1)
	}
	if y > 0 {
		out = append(out, index-goSize)
	}
	if y < goSize-1 {
		out = append(out, index+goSize)
	}
	return out
}

func goGroup(board []int, start int) (stones []int, liberties map[int]bool) {
	color := board[start]
	seen := map[int]bool{start: true}
	queue := []int{start}
	liberties = map[int]bool{}
	for len(queue) > 0 {
		index := queue[0]
		queue = queue[1:]
		stones = append(stones, index)
		for _, n := range goNeighbors(index) {
			switch board[n] {
			case 0:
				liberties[n] = true
			case color:
				if !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				}
			}
		}
	}
	return stones, liberties
}

func (g *gameUI) applyGo(player int, action map[string]any) error {
	if player != g.Turn {
		return fmt.Errorf("还没轮到该座位")
	}
	if str(action, "type") == "pass" {
		g.Turn = 1 - player
		g.Passes++
		if g.Passes >= 2 {
			black, white := scoreGo(g.Board)
			switch {
			case black > white:
				g.Winner = 0
			case white > black:
				g.Winner = 1
			default:
				g.Winner = -1
			}
		}
		return nil
	}
	if str(action, "type") != "place" {
		return fmt.Errorf("操作无效")
	}
	x, y := int(num(action, "x")), int(num(action, "y"))
	if x < 0 || y < 0 || x >= goSize || y >= goSize {
		return fmt.Errorf("落子位置无效")
	}
	index := y*goSize + x
	if g.Board[index] != 0 || g.Ko == index {
		return fmt.Errorf("该位置不能落子")
	}
	next := append([]int(nil), g.Board...)
	next[index] = player + 1
	captured := map[int]bool{}
	for _, n := range goNeighbors(index) {
		if next[n] == 2-player {
			stones, liberties := goGroup(next, n)
			if len(liberties) == 0 {
				for _, stone := range stones {
					captured[stone] = true
				}
			}
		}
	}
	for stone := range captured {
		next[stone] = 0
	}
	own, liberties := goGroup(next, index)
	if len(liberties) == 0 {
		return fmt.Errorf("不能自杀")
	}
	if len(captured) == 1 && len(own) == 1 {
		for stone := range captured {
			g.Ko = stone
		}
	} else {
		g.Ko = -1
	}
	g.Board = next
	g.Passes = 0
	g.Turn = 1 - player
	g.LastIndex = index
	return nil
}

func scoreGo(board []int) (black, white int) {
	for _, v := range board {
		switch v {
		case 1:
			black++
		case 2:
			white++
		}
	}
	seen := map[int]bool{}
	for i, v := range board {
		if v != 0 || seen[i] {
			continue
		}
		queue := []int{i}
		seen[i] = true
		region := []int{}
		colors := map[int]bool{}
		for len(queue) > 0 {
			index := queue[0]
			queue = queue[1:]
			region = append(region, index)
			for _, n := range goNeighbors(index) {
				if board[n] == 0 && !seen[n] {
					seen[n] = true
					queue = append(queue, n)
				} else if board[n] != 0 {
					colors[board[n]] = true
				}
			}
		}
		if len(colors) == 1 {
			if colors[1] {
				black += len(region)
			} else if colors[2] {
				white += len(region)
			}
		}
	}
	return black, white
}

func xiangqiStart() [10][9]string {
	var board [10][9]string
	back := []string{"R", "N", "B", "A", "K", "A", "B", "N", "R"}
	for x := 0; x < 9; x++ {
		board[0][x] = "b" + back[x]
		board[9][x] = "r" + back[x]
	}
	board[2][1], board[2][7] = "bC", "bC"
	board[7][1], board[7][7] = "rC", "rC"
	for _, x := range []int{0, 2, 4, 6, 8} {
		board[3][x] = "bP"
		board[6][x] = "rP"
	}
	return board
}

func isRedPiece(piece string) bool { return strings.HasPrefix(piece, "r") }

func (g *gameUI) applyXiangqi(player int, action map[string]any) error {
	if player != g.Turn || str(action, "type") != "move" {
		return fmt.Errorf("还没轮到该座位")
	}
	fx, fy := int(num(action, "fromX")), int(num(action, "fromY"))
	tx, ty := int(num(action, "toX")), int(num(action, "toY"))
	if fx < 0 || fy < 0 || tx < 0 || ty < 0 || fx > 8 || tx > 8 || fy > 9 || ty > 9 {
		return fmt.Errorf("坐标无效")
	}
	piece := g.XQ[fy][fx]
	if piece == "" || isRedPiece(piece) != (player == 0) {
		return fmt.Errorf("该位置没有你的棋子")
	}
	if !xiangqiMoveOK(g.XQ, fx, fy, tx, ty, piece) {
		return fmt.Errorf("走法不合法")
	}
	next := g.XQ
	next[ty][tx] = piece
	next[fy][fx] = ""
	if xiangqiInCheck(next, player == 0) {
		return fmt.Errorf("不能送将或将帅照面")
	}
	g.XQ = next
	g.Turn = 1 - player
	g.LastIndex = ty*9 + tx
	kings := map[bool]string{true: "rK", false: "bK"}
	if !xiangqiHasPiece(next, kings[player != 0]) {
		g.Winner = player
		return nil
	}
	if !xiangqiHasLegalMove(next, player != 0) {
		if xiangqiInCheck(next, player != 0) {
			g.Winner = player
		} else {
			g.Winner = -1
		}
	}
	return nil
}

func xiangqiHasPiece(board [10][9]string, piece string) bool {
	for y := 0; y < 10; y++ {
		for x := 0; x < 9; x++ {
			if board[y][x] == piece {
				return true
			}
		}
	}
	return false
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func signInt(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}

func xiangqiMoveOK(board [10][9]string, fx, fy, tx, ty int, piece string) bool {
	if fx == tx && fy == ty {
		return false
	}
	dx, dy := tx-fx, ty-fy
	ax, ay := absInt(dx), absInt(dy)
	red := isRedPiece(piece)
	countBetween := func() int {
		n := 0
		x, y := fx+signInt(dx), fy+signInt(dy)
		for x != tx || y != ty {
			if board[y][x] != "" {
				n++
			}
			x += signInt(dx)
			y += signInt(dy)
		}
		return n
	}
	target := board[ty][tx]
	switch piece[1] {
	case 'R':
		return (dx == 0 || dy == 0) && countBetween() == 0
	case 'C':
		want := 0
		if target != "" {
			want = 1
		}
		return (dx == 0 || dy == 0) && countBetween() == want
	case 'N':
		if ax*ay != 2 {
			return false
		}
		legY, legX := fy, fx
		if ay == 2 {
			legY = fy + signInt(dy)
		} else {
			legX = fx + signInt(dx)
		}
		return board[legY][legX] == ""
	case 'B':
		if ax != 2 || ay != 2 || board[fy+dy/2][fx+dx/2] != "" {
			return false
		}
		return red == (ty >= 5)
	case 'A':
		return ax == 1 && ay == 1 && tx >= 3 && tx <= 5 && red == (ty >= 7)
	case 'K':
		if fx == tx && strings.HasSuffix(target, "K") && countBetween() == 0 {
			return true
		}
		return ax+ay == 1 && tx >= 3 && tx <= 5 && red == (ty >= 7)
	case 'P':
		forward := 1
		if red {
			forward = -1
		}
		crossed := fy <= 4
		if red {
			crossed = fy >= 5
		}
		return (dx == 0 && dy == forward) || (crossed && ax == 1 && dy == 0)
	}
	return false
}

func xiangqiInCheck(board [10][9]string, red bool) bool {
	gx, gy := -1, -1
	king := "bK"
	if red {
		king = "rK"
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 9; x++ {
			if board[y][x] == king {
				gx, gy = x, y
			}
		}
	}
	if gx < 0 {
		return true
	}
	for y := 0; y < 10; y++ {
		for x := 0; x < 9; x++ {
			piece := board[y][x]
			if piece == "" || isRedPiece(piece) == red {
				continue
			}
			if xiangqiMoveOK(board, x, y, gx, gy, piece) {
				return true
			}
		}
	}
	otherY := 9
	if red {
		otherY = 0
	}
	if strings.HasSuffix(board[otherY][gx], "K") {
		clear := true
		for y := minInt(gy, otherY) + 1; y < maxInt(gy, otherY); y++ {
			if board[y][gx] != "" {
				clear = false
			}
		}
		if clear {
			return true
		}
	}
	return false
}

func xiangqiHasLegalMove(board [10][9]string, red bool) bool {
	for fy := 0; fy < 10; fy++ {
		for fx := 0; fx < 9; fx++ {
			piece := board[fy][fx]
			if piece == "" || isRedPiece(piece) != red {
				continue
			}
			for ty := 0; ty < 10; ty++ {
				for tx := 0; tx < 9; tx++ {
					if board[ty][tx] != "" && isRedPiece(board[ty][tx]) == red {
						continue
					}
					if !xiangqiMoveOK(board, fx, fy, tx, ty, piece) {
						continue
					}
					next := board
					next[ty][tx] = piece
					next[fy][fx] = ""
					if !xiangqiInCheck(next, red) {
						return true
					}
				}
			}
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---- Doudizhu ------------------------------------------------------------

func (a *desktop) ddzDeal() {
	g := &a.gameF
	deck := makeDeck()
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	g.Hands = [3][]int{sortCards(deck[0:17]), sortCards(deck[17:34]), sortCards(deck[34:51])}
	g.Bottom = sortCards(deck[51:])
	g.Phase = ddzPhaseBid
	g.Turn = 0
	g.Landlord = -1
	g.LastCards = nil
	g.LastPlayer = -1
	g.SelectedCards = nil
	g.Winner = -1
	g.Started = true
	g.Thinking = false
	g.Status = "叫地主：轮到你"
}

func makeDeck() []int {
	var deck []int
	for v := 3; v <= 15; v++ {
		for n := 0; n < 4; n++ {
			deck = append(deck, v)
		}
	}
	return append(deck, 16, 17)
}

func sortCards(cards []int) []int {
	out := append([]int(nil), cards...)
	sort.Ints(out)
	return out
}

func (a *desktop) ddzBid(player int, call bool) {
	g := &a.gameF
	if g.Phase != ddzPhaseBid {
		return
	}
	if player != g.Turn {
		return
	}
	if call || player == 2 {
		g.Landlord = player
		g.Hands[player] = sortCards(append(g.Hands[player], g.Bottom...))
		g.Phase = ddzPhasePlay
		g.Turn = player
		g.Status = fmt.Sprintf("座位 %d 是地主，出牌", player+1)
		a.ddzAIIfNeeded()
		return
	}
	g.Turn = (player + 1) % 3
	g.Status = fmt.Sprintf("座位 %d 决定中…", g.Turn+1)
	a.ddzAIIfNeeded()
}

func (a *desktop) ddzAIIfNeeded() {
	g := &a.gameF
	if g.Winner >= 0 {
		return
	}
	if g.Turn == 0 {
		if g.Phase == ddzPhaseBid {
			g.Status = "叫地主：轮到你"
		} else {
			g.Status = "轮到你出牌"
		}
		return
	}
	g.Thinking = true
	g.Status = fmt.Sprintf("座位 %d 思考中…", g.Turn+1)
	seat := g.Turn
	cfg, ok := a.gameModelConfig(g.ModelA)
	if !ok {
		g.Thinking = false
		g.Status = "请选择可用模型"
		return
	}
	system, user := a.gamePrompt(seat)
	a.gameAIRequest(seat, cfg, system, user, 0)
}

func (a *desktop) ddzPlay(player int, cards []int) {
	g := &a.gameF
	if err := g.applyDoudizhu(player, map[string]any{"type": "play", "cards": jsonCards(cards)}); err != nil {
		g.Status = err.Error()
		return
	}
	g.SelectedCards = nil
	if g.Winner >= 0 {
		a.gameFinished()
		return
	}
	a.ddzAIIfNeeded()
}

func (a *desktop) ddzPass(player int) {
	g := &a.gameF
	if err := g.applyDoudizhu(player, map[string]any{"type": "pass"}); err != nil {
		g.Status = err.Error()
		return
	}
	a.ddzAIIfNeeded()
}

func jsonCards(cards []int) []any {
	out := make([]any, 0, len(cards))
	for _, card := range cards {
		out = append(out, float64(card))
	}
	return out
}

func (g *gameUI) applyDoudizhu(player int, action map[string]any) error {
	switch g.Phase {
	case ddzPhaseDeal:
		return fmt.Errorf("等待房主发牌")
	case ddzPhaseBid:
		if player != g.Turn || str(action, "type") != "bid" {
			return fmt.Errorf("叫地主操作无效")
		}
		value := int(num(action, "value"))
		g.Turn = (player + 1) % 3
		if value == 1 {
			g.Landlord = player
			g.Hands[player] = sortCards(append(g.Hands[player], g.Bottom...))
			g.Phase = ddzPhasePlay
			g.Turn = player
		} else if g.Turn == 0 {
			g.Landlord = 0
			g.Hands[0] = sortCards(append(g.Hands[0], g.Bottom...))
			g.Phase = ddzPhasePlay
			g.Turn = 0
		}
		return nil
	}
	if player != g.Turn {
		return fmt.Errorf("还没轮到该座位")
	}
	if str(action, "type") == "pass" {
		if len(g.LastCards) == 0 {
			return fmt.Errorf("第一手不能过")
		}
		g.DDZPasses++
		g.Turn = (player + 1) % 3
		if g.DDZPasses >= 2 {
			g.LastCards = nil
			g.DDZPasses = 0
		}
		return nil
	}
	if str(action, "type") != "play" {
		return fmt.Errorf("出牌不合法")
	}
	var cards []int
	for _, item := range list(action, "cards") {
		cards = append(cards, int(num(map[string]any{"v": item}, "v")))
	}
	if !validCards(cards, g.Hands[player]) || !beatsCards(cards, g.LastCards) {
		return fmt.Errorf("出牌不合法")
	}
	g.Hands[player] = removeCards(g.Hands[player], cards)
	g.LastCards = append([]int(nil), cards...)
	g.LastPlayer = player
	g.DDZPasses = 0
	g.Turn = (player + 1) % 3
	if len(g.Hands[player]) == 0 {
		g.Winner = player
	}
	return nil
}

func validCards(cards, hand []int) bool {
	if len(cards) == 0 {
		return false
	}
	copyHand := append([]int(nil), hand...)
	for _, card := range cards {
		found := false
		for i, h := range copyHand {
			if h == card {
				copyHand = append(copyHand[:i], copyHand[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return comboType(cards) != ""
}

func removeCards(hand, cards []int) []int {
	out := append([]int(nil), hand...)
	for _, card := range cards {
		for i, h := range out {
			if h == card {
				out = append(out[:i], out[i+1:]...)
				break
			}
		}
	}
	return out
}

type combo struct {
	kind   string
	main   int
	length int
}

func comboType(cards []int) string { return rankCards(cards).kind }

func rankCards(cards []int) combo {
	sorted := append([]int(nil), cards...)
	sort.Ints(sorted)
	counts := map[int]int{}
	for _, c := range sorted {
		counts[c]++
	}
	var values []int
	for v := range counts {
		values = append(values, v)
	}
	sort.Ints(values)
	with := func(n int) []int {
		var out []int
		for _, v := range values {
			if counts[v] == n {
				out = append(out, v)
			}
		}
		return out
	}
	consecutive := func(vs []int) bool {
		if len(vs) == 0 || vs[len(vs)-1] >= 15 {
			return false
		}
		for i := 1; i < len(vs); i++ {
			if vs[i] != vs[i-1]+1 {
				return false
			}
		}
		return true
	}
	n := len(sorted)
	if n == 2 && counts[16] == 1 && counts[17] == 1 {
		return combo{"rocket", 17, 2}
	}
	if n == 4 && len(with(4)) == 1 {
		return combo{"bomb", with(4)[0], 4}
	}
	if n == 1 {
		return combo{"single", sorted[0], 1}
	}
	if n == 2 && len(with(2)) == 1 {
		return combo{"pair", with(2)[0], 2}
	}
	if n == 3 && len(with(3)) == 1 {
		return combo{"triple", with(3)[0], 3}
	}
	if n == 4 && len(with(3)) == 1 {
		return combo{"triple_single", with(3)[0], 4}
	}
	if n == 5 && len(with(3)) == 1 && len(with(2)) == 1 {
		return combo{"triple_pair", with(3)[0], 5}
	}
	if n >= 5 && len(with(1)) == n && consecutive(values) {
		return combo{"straight", sorted[n-1], n}
	}
	if n >= 6 && n%2 == 0 && len(with(2))*2 == n && consecutive(with(2)) {
		return combo{"double_straight", with(2)[len(with(2))-1], n}
	}
	for _, wing := range []string{"none", "single", "pair"} {
		unit := map[string]int{"none": 3, "single": 4, "pair": 5}[wing]
		if n%unit != 0 {
			continue
		}
		triples := n / unit
		if triples < 2 {
			continue
		}
		var candidates []int
		for _, v := range values {
			if v < 15 && counts[v] >= 3 {
				candidates = append(candidates, v)
			}
		}
		for start := 0; start+triples <= len(candidates); start++ {
			sequence := candidates[start : start+triples]
			if !consecutive(sequence) {
				continue
			}
			remain := map[int]int{}
			for v, c := range counts {
				remain[v] = c
			}
			for _, v := range sequence {
				remain[v] -= 3
			}
			var leftovers []int
			for _, c := range remain {
				if c > 0 {
					leftovers = append(leftovers, c)
				}
			}
			ok := true
			switch wing {
			case "none":
				ok = len(leftovers) == 0
			case "single":
				ok = len(leftovers) == triples
				for _, c := range leftovers {
					if c != 1 {
						ok = false
					}
				}
			case "pair":
				ok = len(leftovers) == triples
				for _, c := range leftovers {
					if c != 2 {
						ok = false
					}
				}
			}
			if ok {
				return combo{"plane_" + wing, sequence[len(sequence)-1], n}
			}
		}
	}
	if n == 6 && len(with(4)) == 1 {
		return combo{"four_two_single", with(4)[0], 6}
	}
	if n == 8 && len(with(4)) == 1 && len(with(2)) == 2 {
		return combo{"four_two_pair", with(4)[0], 8}
	}
	return combo{}
}

func beatsCards(cards, last []int) bool {
	if len(last) == 0 {
		return rankCards(cards).kind != ""
	}
	a, b := rankCards(cards), rankCards(last)
	if a.kind == "" {
		return false
	}
	if a.kind == "rocket" {
		return true
	}
	if b.kind == "rocket" {
		return false
	}
	if a.kind == "bomb" && b.kind != "bomb" {
		return true
	}
	return a.kind == b.kind && a.length == b.length && a.main > b.main
}

// ---- Prompts -------------------------------------------------------------

func (a *desktop) gamePrompt(seat int) (string, string) {
	g := &a.gameF
	switch g.Kind {
	case gameGomoku:
		system := "你在和一个人类下五子棋（15×15 棋盘）。双方轮流在空交叉点落子，先在横、竖或斜任一方向连成五子（含以上）者获胜。\n棋盘用文本表示：'.' 是空点，'X' 是黑子，'O' 是白子。列号 1-15 从左到右，行号 1-15 从上到下。黑先行。\n轮到你时只输出一个 JSON 对象：{\"type\":\"place\",\"x\":<列号1-15>,\"y\":<行号1-15>}，除此之外不要输出任何文字。"
		user := "当前局面：\n" + renderGridBoard(g.Board, gomokuSize, map[int]string{0: ".", 1: "X", 2: "O"}) + fmt.Sprintf("\n你执%s。", map[bool]string{true: "白（O）", false: "黑（X）"}[seat == 1])
		return system, user
	case gameGo:
		system := "你在和一个人类下围棋（9×9 小棋盘）。黑先行。'.' 是空点，'X' 是黑子，'O' 是白子；列号 1-9 从左到右，行号 1-9 从上到下。\n规则：落子后无气的对方棋群会被提掉；不能自杀；打劫时不得立即提回。双方连续各停一手则终局。\n轮到你时只输出一个 JSON 对象：落子 {\"type\":\"place\",\"x\":<1-9>,\"y\":<1-9>}，或停一手 {\"type\":\"pass\"}。除此之外不要输出任何文字。"
		user := "当前局面：\n" + renderGridBoard(g.Board, goSize, map[int]string{0: ".", 1: "X", 2: "O"}) + fmt.Sprintf("\n你执%s。", map[bool]string{true: "白（O）", false: "黑（X）"}[seat == 1])
		return system, user
	case gameXiangqi:
		system := "你和人类下中国象棋。棋盘 9 列 × 10 行：列 1-9 从左到右，行 1-10 从上到下；红在下方先行。棋盘文本中红子用汉字（车 马 相 仕 帅 炮 兵），黑子加括号。只输出一个 JSON 对象：{\"type\":\"move\",\"fromX\":<1-9>,\"fromY\":<1-10>,\"toX\":<1-9>,\"toY\":<1-10>}，除此之外不要输出任何文字。"
		user := "当前局面：\n" + renderXiangqiBoard(g.XQ) + fmt.Sprintf("\n你执%s。", map[bool]string{true: "黑", false: "红"}[seat == 1])
		return system, user
	}
	system := "你在玩斗地主。牌面值 3-15 分别代表 3-2，16 是小王，17 是大王。叫地主阶段输出 {\"type\":\"bid\",\"value\":1} 或 {\"type\":\"bid\",\"value\":0}；出牌阶段输出 {\"type\":\"play\",\"cards\":[牌面值...]} 或 {\"type\":\"pass\"}。只输出一个 JSON 对象。"
	var hand []string
	for _, card := range g.Hands[seat] {
		hand = append(hand, cardName(card))
	}
	user := fmt.Sprintf("你的手牌：%s\n上一手：%s\n你的座位：%d", strings.Join(hand, " "), cardList(g.LastCards), seat+1)
	return system, user
}

func renderGridBoard(board []int, size int, symbols map[int]string) string {
	var b strings.Builder
	b.WriteString("     ")
	for x := 0; x < size; x++ {
		b.WriteString(fmt.Sprintf("%2d ", x+1))
	}
	b.WriteString("\n")
	for y := 0; y < size; y++ {
		b.WriteString(fmt.Sprintf("行%2d ", y+1))
		for x := 0; x < size; x++ {
			b.WriteString(" " + symbols[board[y*size+x]] + " ")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderXiangqiBoard(board [10][9]string) string {
	var b strings.Builder
	b.WriteString("      1 2 3 4 5 6 7 8 9  ←列\n")
	for y := 0; y < 10; y++ {
		b.WriteString(fmt.Sprintf("行%2d ", y+1))
		for x := 0; x < 9; x++ {
			piece := board[y][x]
			if piece == "" {
				b.WriteString(" ·")
				continue
			}
			glyph := xiangqiGlyph(piece)
			if isRedPiece(piece) {
				b.WriteString(" " + glyph)
			} else {
				b.WriteString("(" + glyph + ")")
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

var _ = rand.Int
