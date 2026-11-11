package desktop

import (
	"testing"
	"time"
)

func TestImagePreviewDecodeWaiterCancels(t *testing.T) {
	slot := make(chan struct{}, 1)
	slot <- struct{}{}
	cancel := make(chan struct{})
	result := make(chan bool, 1)
	go func() { result <- acquireImagePreviewDecode(slot, cancel) }()
	close(cancel)
	select {
	case acquired := <-result:
		if acquired {
			t.Fatal("cancelled preview started decoding")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled preview remained blocked")
	}
	<-slot
	if acquireImagePreviewDecode(slot, cancel) || len(slot) != 0 {
		t.Fatal("cancelled preview retained a decode slot")
	}
	if !acquireImagePreviewDecode(slot, make(chan struct{})) {
		t.Fatal("active preview could not decode")
	}
	<-slot
}
