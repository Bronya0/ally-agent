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

func TestProtocolContainerFencesKeepExamplesAndResume(t *testing.T) {
	t.Parallel()
	example := "<msg to='peer' type='finding'>example</msg>"
	actual := "<msg to='peer' type='finding'>actual</msg>"
	for name, text := range map[string]string{
		"bullet tilde":        "- ~~~xml\n  " + example + "\n  ~~~\n",
		"bullet backtick":     "* ```xml\n  " + example + "\n  ```\n",
		"plus bullet":         "+ ~~~xml\n  " + example + "\n  ~~~\n",
		"ordered dot":         "123. ~~~xml\n     " + example + "\n     ~~~\n",
		"ordered paren":       "12) ```xml\n    " + example + "\n    ```\n",
		"padded marker":       "-    ~~~xml\n     " + example + "\n     ~~~\n",
		"tab padding":         "-\t~~~xml\n\t" + example + "\n\t~~~\n",
		"continued list":      "- example\n    ~~~xml\n  " + example + "\n    ~~~\n",
		"continued ordered":   "123. example\n     ~~~xml\n     " + example + "\n     ~~~\n",
		"nested list":         "- outer\n  - inner\n    ~~~xml\n    " + example + "\n    ~~~\n",
		"nested quote":        "> > ~~~xml\n> > " + example + "\n> > ~~~\n",
		"quote list":          "> - ~~~xml\n>   " + example + "\n>   ~~~\n",
		"list quote":          "- > ```xml\n  > " + example + "\n  > ```\n",
		"quoted continuation": "> - example\n>     ~~~xml\n>   " + example + "\n>     ~~~\n",
		"list fence content":  "- ~~~xml\n  - ~~~\n  " + example + "\n  ~~~\n",
		"root fence content":  "~~~xml\n- ~~~\n" + example + "\n~~~\n",
		"long nested fence":   "- ~~~~xml\n  ~~~\n  " + example + "\n  ~~~~~\n",
		"blank in list":       "- ~~~xml\n\n  " + example + "\n  ~~~\n",
		"quote ends":          "> ~~~xml\n> " + example + "\n",
		"list ends":           "- ~~~xml\n  " + example + "\n",
		"CRLF list":           "- ~~~xml\r\n  " + example + "\r\n  ~~~\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			var messages []Message
			clean := Intercept(text+actual+"\nordinary text", func(message Message) bool {
				messages = append(messages, message)
				return true
			})
			if len(messages) != 1 || messages[0].Body != "actual" || clean != text+"\nordinary text" {
				t.Fatalf("container fence leaked or failed to resume: messages %+v, output %q", messages, clean)
			}
		})
	}
}

func TestProtocolHTMLCodeTagsKeepExamplesAndResume(t *testing.T) {
	t.Parallel()
	example := "<msg to='peer' type='finding'>example</msg>"
	actual := "<msg to='peer' type='finding'>actual</msg>"
	for _, tag := range []string{"pre", "code", "script", "style", "textarea"} {
		for name, text := range map[string]string{
			"plain":                 "<" + tag + ">\n" + example + "\n</" + tag + ">\n",
			"multiline opening":     "<" + tag + "\n class=\"example\">\n" + example + "\n</" + tag + ">\n",
			"CRLF opening":          "<" + tag + "\r\n class=\"example\">\r\n" + example + "\r\n</" + tag + ">\r\n",
			"tab opening":           "<" + tag + "\tclass='example'>\n" + example + "\n</" + tag + ">\n",
			"formfeed opening":      "<" + tag + "\fclass='example'>\n" + example + "\n</" + tag + ">\n",
			"mixed case":            "<" + strings.ToUpper(tag) + "\n class='example'>\n" + example + "\n</" + strings.ToUpper(tag) + " >\n",
			"multiline closing":     "<" + tag + ">\n" + example + "\n</" + tag + "\n >\n",
			"quoted closing":        "<" + tag + " data-example='</" + tag + ">'>\n" + example + "\n</" + tag + ">\n",
			"multiline attribute":   "<" + tag + " data-example='\n</" + tag + ">\n'>\n" + example + "\n</" + tag + ">\n",
			"multiple blocks":       "<" + tag + "></" + tag + "><" + tag + "\n class='example'>\n" + example + "\n</" + tag + ">\n",
			"wrong closing name":    "<" + tag + ">\n</" + tag + "lude>\n" + example + "\n</" + tag + ">\n",
			"literal angle in code": "<" + tag + ">\nif a <b then\n" + example + "\n</" + tag + ">\n",
		} {
			t.Run(tag+"/"+name, func(t *testing.T) {
				var messages []Message
				clean := Intercept(text+actual+"\nordinary text", func(message Message) bool {
					messages = append(messages, message)
					return true
				})
				if len(messages) != 1 || messages[0].Body != "actual" || clean != text+"\nordinary text" {
					t.Fatalf("HTML example leaked or failed to resume: messages %+v, output %q", messages, clean)
				}
			})
		}
	}
}

func TestProtocolHTMLCodeNestingAndTagBoundaries(t *testing.T) {
	t.Parallel()
	example := "<msg to='peer' type='finding'>example</msg>"
	actual := "<msg to='peer' type='finding'>actual</msg>"
	for name, text := range map[string]string{
		"pre with code":      "<pre><code>\n" + example + "\n</code>\n" + example + "\n</pre>\n",
		"nested pre":         "<pre><pre>\n" + example + "\n</pre>\n" + example + "\n</pre>\n",
		"nested code":        "<code><code>\n" + example + "\n</code>\n" + example + "\n</code>\n",
		"body attribute":     "<pre><span title='</pre>'>\n" + example + "\n</span></pre>\n",
		"pre comment":        "<pre><!-- </pre> -->\n" + example + "\n</pre>\n",
		"code comment":       "<code><!--\n</code>\n-->\n" + example + "\n</code>\n",
		"escaped HTML":       "\\<pre>\n",
		"inline HTML code":   "`<pre>`\n",
		"different tag name": "<preview>\n",
		"closed inline HTML": "<pre>example</pre>\n",
	} {
		t.Run(name, func(t *testing.T) {
			var messages []Message
			clean := Intercept(text+actual, func(message Message) bool {
				messages = append(messages, message)
				return true
			})
			if len(messages) != 1 || messages[0].Body != "actual" || clean != text {
				t.Fatalf("HTML boundary handling: messages %+v, output %q", messages, clean)
			}
		})
	}
}

func TestProtocolInlineCodeSpanDoesNotOutliveItsLine(t *testing.T) {
	t.Parallel()
	actual := "<msg to='peer' type='finding'>actual</msg>"
	// A code span ends with its line, so an unmatched backtick in prose is literal
	// text and the next line's message must still be intercepted.
	for _, text := range []string{"use `rg then\n", "a `b` c\n", "`\n"} {
		var messages []Message
		clean := Intercept(text+actual, func(message Message) bool {
			messages = append(messages, message)
			return true
		})
		if len(messages) != 1 || messages[0].Body != "actual" || clean != text {
			t.Fatalf("unmatched backtick hid the next line: text %q, messages %+v, output %q", text, messages, clean)
		}
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
