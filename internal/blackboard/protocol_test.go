// SPDX-License-Identifier: GPL-3.0-only
package blackboard

import (
	"reflect"
	"strings"
	"testing"
)

func TestProtocolValidBlocksPreserveOrdinaryText(t *testing.T) {
	t.Parallel()
	text := "普通结果\r\n" +
		"<msg to=\"agent-b\" type=\"finding\">\nJWT &amp; Token 已支持。\n</msg>\r\n" +
		"  <msg to='*' type='decision'>迁移已完成。</msg><msg to='agent-a' type='answer' reply_to='12'><![CDATA[确认，字面量 </msg> 不是结束。]]></msg>\r\n" +
		"保留工具执行结论"
	var messages []Message
	clean := Intercept(text, func(message Message) bool {
		messages = append(messages, message)
		return true
	})
	if clean != "普通结果\r\n\r\n  \r\n保留工具执行结论" {
		t.Fatalf("ordinary output changed: %q", clean)
	}
	if len(messages) != 3 || messages[0].Body != "JWT & Token 已支持。" || messages[1].To != "*" || messages[2].ReplyTo == nil || *messages[2].ReplyTo != 12 || messages[2].Body != "确认，字面量 </msg> 不是结束。" {
		t.Fatalf("parsed messages = %+v", messages)
	}
	for _, message := range messages {
		if message.From != "" || message.Seq != 0 {
			t.Fatalf("parser supplied a runtime-owned identity: %+v", message)
		}
	}
}

func TestProtocolMalformedMessagesStayInNormalOutput(t *testing.T) {
	t.Parallel()
	for name, text := range map[string]string{
		"missing recipient":      "<msg type='finding'>fact</msg>",
		"missing type":           "<msg to='agent-b'>fact</msg>",
		"unknown type":           "<msg to='agent-b' type='progress'>fact</msg>",
		"bad recipient":          "<msg to='agent b' type='finding'>fact</msg>",
		"empty body":             "<msg to='*' type='finding'> \n\t </msg>",
		"sender spoof":           "<msg to='*' type='finding' from='agent-c'>fact</msg>",
		"sequence spoof":         "<msg to='*' type='finding' seq='99'>fact</msg>",
		"unknown attr":           "<msg to='*' type='finding' surprise='yes'>fact</msg>",
		"duplicate attr":         "<msg to='agent-b' to='agent-c' type='finding'>fact</msg>",
		"namespace":              "<msg xmlns='urn:example' to='*' type='finding'>fact</msg>",
		"namespaced attr":        "<msg xmlns:x='urn:example' x:to='agent-b' type='finding'>fact</msg>",
		"nested markup":          "<msg to='*' type='finding'><b>fact</b></msg>",
		"nested message":         "<msg to='*' type='finding'>\n<msg to='agent-b' type='finding'>example</msg>\n</msg>",
		"comment body":           "<msg to='*' type='finding'><!-- hidden -->fact</msg>",
		"processing instruction": "<msg to='*' type='finding'><?other instruction?>fact</msg>",
		"directive":              "<msg to='*' type='finding'><!DOCTYPE msg>fact</msg>",
		"bad entity":             "<msg to='*' type='finding'>A & B</msg>",
		"unknown entity":         "<msg to='*' type='finding'>&external;</msg>",
		"unclosed":               "<msg to='*' type='finding'>fact",
		"mismatched":             "<msg to='*' type='finding'>fact</other>",
		"self closing":           "<msg to='*' type='finding'/>",
		"negative reply":         "<msg to='*' type='answer' reply_to='-1'>fact</msg>",
		"zero reply":             "<msg to='*' type='answer' reply_to='0'>fact</msg>",
		"bad reply":              "<msg to='*' type='answer' reply_to='not-a-seq'>fact</msg>",
		"overflow reply":         "<msg to='*' type='answer' reply_to='9223372036854775808'>fact</msg>",
		"oversized":              "<msg to='*' type='finding'>" + strings.Repeat("x", MaxMessageBytes) + "</msg>",
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			clean := Intercept(text, func(Message) bool { called = true; return true })
			if called || clean != text {
				t.Fatalf("invalid XML was published or removed: called %v, output %q", called, clean)
			}
		})
	}
}

func TestProtocolIgnoresExamplesAndEscapedTags(t *testing.T) {
	t.Parallel()
	message := "<msg to='agent-b' type='finding'>example, not a publication</msg>"
	for name, text := range map[string]string{
		"backtick fence":        "```xml\n" + message + "\n```",
		"tilde fence":           "~~~xml\n" + message + "\n~~~",
		"long fence":            "````xml\n```\n" + message + "\n````",
		"indented fence":        "   ```xml\n" + message + "\n   ```",
		"indented code":         "    " + message,
		"tab code":              "\t" + message,
		"mixed indent":          "   \t" + message,
		"inline code":           "`" + message + "`",
		"multiline inline code": "``example\n" + message + "\n``",
		"inline prose":          "Here is an example: " + message,
		"suffix prose":          message + " is an example.",
		"quote":                 "> " + message,
		"escape":                "\\" + message,
		"escaped entity":        "&lt;msg to='agent-b' type='finding'&gt;example&lt;/msg&gt;",
		"HTML comment":          "<!--\n" + message + "\n-->",
		"HTML pre":              "<pre>\n" + message + "\n</pre>",
		"HTML code":             "<code>\n" + message + "\n</code>",
		"other element":         "<messages to='agent-b' type='finding'>not msg</messages>",
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			clean := Intercept(text, func(Message) bool { called = true; return true })
			if called || clean != text {
				t.Fatalf("example interpreted as protocol: called %v, output %q", called, clean)
			}
		})
	}
}

func TestProtocolResumesAfterExamplesAndRejectedBlocks(t *testing.T) {
	t.Parallel()
	text := "```xml\n<msg to='*' type='finding'>example</msg>\n```\n" +
		"<msg to='agent-b' type='unknown'>invalid type</msg>\n" +
		"<msg to='agent-b' type='finding'>bad & entity</msg>\n" +
		"<msg to='agent-b' type='finding'>good</msg>\n" +
		"<!--\n<msg to='*' type='finding'>comment</msg>\n-->\n" +
		"<msg to='agent-c' type='blocker'>publisher declines</msg>\n" +
		"<msg to='*' type='handoff'>last</msg>"
	var published []string
	clean := Intercept(text, func(message Message) bool {
		if message.To == "agent-c" {
			return false
		}
		published = append(published, message.Body)
		return true
	})
	if !reflect.DeepEqual(published, []string{"good", "last"}) {
		t.Fatalf("published = %v", published)
	}
	for _, retained := range []string{"example</msg>", "invalid type</msg>", "bad & entity</msg>", "comment</msg>", "publisher declines</msg>"} {
		if !strings.Contains(clean, retained) {
			t.Errorf("ordinary/rejected text was removed: %q", retained)
		}
	}
	if strings.Contains(clean, ">good</msg>") || strings.Contains(clean, ">last</msg>") {
		t.Fatalf("accepted protocol remained in output: %s", clean)
	}
}

func TestProtocolStandaloneBoundariesAndEntities(t *testing.T) {
	t.Parallel()
	text := "before\n<msg\n to=\"agent-b\"\n type=\"question\">&lt;a&gt; &quot;b&quot; &apos;c&apos; &#x4e2d;</msg >\nafter"
	var messages []Message
	clean := Intercept(text, func(message Message) bool { messages = append(messages, message); return true })
	if clean != "before\n\nafter" || len(messages) != 1 || messages[0].Body != "<a> \"b\" 'c' 中" {
		t.Fatalf("multiline XML/entity parsing: %q, %+v", clean, messages)
	}
	if got := Intercept(text, nil); got != text {
		t.Fatal("nil publisher changed ordinary output")
	}
}
