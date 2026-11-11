package uikit

import "github.com/egoist/mygo/ui"

// UI kit messages remain independent of Ally and never translate caller data.
// Keep value IDs separate from labels so a language switch cannot change a value.
var kitMessages = map[string][4]string{
	"自动":     {"Auto", "自动", "自動", "자동"},
	"展开选项":   {"Expand options", "展开选项", "選択肢を開く", "옵션 펼치기"},
	"选择或输入":  {"Select or type", "选择或输入", "選択または入力", "선택 또는 입력"},
	"显示 ":    {"Show ", "显示 ", "表示: ", "표시: "},
	"隐藏 ":    {"Hide ", "隐藏 ", "非表示: ", "숨기기: "},
	"移除 ":    {"Remove ", "移除 ", "削除: ", "제거: "},
	"运行中\n":  {"Running\n", "运行中\n", "実行中\n", "실행 중\n"},
	"关闭标签":   {"Close tab", "关闭标签", "タブを閉じる", "탭 닫기"},
	"复制错误全文": {"Copy full error", "复制错误全文", "エラー全文をコピー", "전체 오류 복사"},
}

func kitText(c *ui.Context, source string) string {
	translations, ok := kitMessages[source]
	if !ok {
		return source
	}
	index := 0
	switch c.Locale() {
	case "zh-Hans":
		index = 1
	case "ja":
		index = 2
	case "ko":
		index = 3
	}
	return translations[index]
}
