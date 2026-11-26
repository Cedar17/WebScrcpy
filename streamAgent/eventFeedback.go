package sagent

import (
	"iter"
	"log"
	"webscreen/sdriver"
)

// sdriver -> Agent - > WebMaster -> 前端
// FeedbackEventsUntil stops even when no control event arrives.
func (sa *Agent) FeedbackEventsUntil(done <-chan struct{}) iter.Seq[[]byte] {
	return func(yield func([]byte) bool) {
		for {
			var event sdriver.Event
			select {
			case <-done:
				return
			case e, ok := <-sa.controlCh:
				if !ok { return }
				event = e
			}
			var content []byte
			switch e := event.(type) {
			case sdriver.ReceiveClipboardEvent:
				content = e.Content
			case sdriver.TextMsgEvent:
				content = []byte(e.Msg)
			default:
				log.Printf("Unhandled event type in ReceiveEvent: %d", event.Type())
				continue
			}
			msg := append([]byte{byte(event.Type())}, content...)
			if !yield(msg) {
				return
			}
		}
	}
}

func (sa *Agent) ServeVideoStream() {
 if sa.videoCh == nil { return }
 if sa.OnVideoEnd != nil { defer sa.OnVideoEnd() }
 for box := range sa.videoCh {
  if sa.OnVideoBox != nil { sa.OnVideoBox(box) }
 }
}
func (sa *Agent) ServeAudioStream() {
 if sa.audioCh == nil { return }
 for box := range sa.audioCh {
  if sa.OnAudioBox != nil { sa.OnAudioBox(box) }
 }
}
