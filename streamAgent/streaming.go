package sagent
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
