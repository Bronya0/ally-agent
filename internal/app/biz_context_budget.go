// SPDX-License-Identifier: GPL-3.0-only
package app

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"ally-dev/locales"
	openai "github.com/sashabaranov/go-openai"
)

// AutomaticContextBudgetBytes bounds automatic prefixes and MCP catalogs, not
// user messages, attachments, tool results, or protocol tool schemas.
const AutomaticContextBudgetBytes = 256 * 1024

type automaticContextPrefixKey struct{}

type automaticContextBudgetError struct{ message string }

func (err *automaticContextBudgetError) Error() string { return err.message }

// Keep at most one complete rune beyond the budget. An oversized source stays
// detectably oversized without allocating its entire body. The request guard
// rejects it; this bounded preview must never become a silently truncated prompt.
func appendAutomaticContextText(b *strings.Builder, text string) {
	remaining := AutomaticContextBudgetBytes + 1 - b.Len()
	if remaining <= 0 {
		return
	}
	if len(text) > remaining {
		end := remaining
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end++
		}
		text = text[:end]
	}
	b.WriteString(text)
}

func validateAutomaticContext(ctx context.Context, messages []openai.ChatCompletionMessage) error {
	prefix := leadingSystemCount(messages)
	if count, ok := ctx.Value(automaticContextPrefixKey{}).(int); ok {
		prefix = min(len(messages), max(prefix, count))
	}
	remaining := AutomaticContextBudgetBytes
	for i, message := range messages {
		if i >= prefix && !isMcpCatalogMessage(message) {
			continue
		}
		if len(message.Content) > remaining {
			return &automaticContextBudgetError{message: fmt.Sprintf(locales.Text("自动加载上下文超过 %d KiB，未发送模型请求。请精简项目指令、自定义提示词或清单，或在设置中关闭不需要的加载项后新建会话；不会静默丢弃规则或清单项。"), AutomaticContextBudgetBytes/1024)}
		}
		remaining -= len(message.Content)
	}
	return nil
}
