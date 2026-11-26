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
			eType := event.Type()
			switch eType {
			case sdriver.EVENT_TYPE_RECEIVE_CLIPBOARD:
				event := event.(sdriver.SampleEvent)
				content := event.GetContent()
				msg := make([]byte, 1+len(content))
				copy(msg[1:], content)
				msg[0] = byte(sdriver.EVENT_TYPE_RECEIVE_CLIPBOARD)
				if !yield(msg) {
					return
				}
			case sdriver.EVENT_TYPE_TEXT_MSG:
				event := event.(sdriver.TextMsgEvent)
				content := []byte(event.Msg)
				msg := make([]byte, 1+len(content))
				copy(msg[1:], content)
				msg[0] = byte(sdriver.EVENT_TYPE_TEXT_MSG)
				if !yield(msg) {
					return
				}
			default:
				log.Printf("Unhandled event type in ReceiveEvent: %d", eType)
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
