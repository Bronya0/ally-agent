package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAskImagesReachModelByQuestion(t *testing.T) {
	req := AskRequest{Questions: []AskQuestion{{ID: "q1", Question: "First"}, {ID: "q2", Question: "Second"}}}
	first := "data:image/png;base64,YWJj"
	second := "data:image/png;base64,ZGVm"
	result, err := resolveAskSubmission("ask", req, []AskSubmittedAnswer{{QuestionID: "q1", Images: []string{first, second}}, {QuestionID: "q2", CustomText: "answer"}})
	if err != nil {
		t.Fatal(err)
	}
	images := collectReadImages("ask", &toolResult{OK: true, Data: result})
	if len(images) != 2 || images[0].DataURL != first || images[1].DataURL != second || !strings.Contains(images[0].Path, "q1") {
		t.Fatalf("images = %#v", images)
	}
	message := readImageInjectionMessage(images)
	if message == nil || len(message.MultiContent) != 3 || message.MultiContent[2].ImageURL.URL != second {
		t.Fatal("images missing from model message")
	}
	encoded, err := json.Marshal(result)
	if err != nil || strings.Contains(string(encoded), first) || !strings.Contains(string(encoded), `"imageCount":2`) {
		t.Fatalf("unexpected result: %s, %v", encoded, err)
	}
	if !strings.Contains(renderAskResultForModel(result), "attached images: 2") {
		t.Fatal("model result missing image count")
	}
}

func TestAskRejectsInvalidImages(t *testing.T) {
	req := AskRequest{Questions: []AskQuestion{{ID: "q1", Question: "Question"}}}
	for _, images := range [][]string{{"data:image/png;base64,%%%"}, make([]string, 9)} {
		if _, err := resolveAskSubmission("ask", req, []AskSubmittedAnswer{{QuestionID: "q1", Images: images}}); err == nil {
			t.Fatal("invalid images accepted")
		}
	}
}
