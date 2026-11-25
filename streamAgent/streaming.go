package sagent
func (sa *Agent) ServeVideoStream() {
 if sa.videoCh == nil { return }
 if sa.OnVideoEnd != nil { defer sa.OnVideoEnd() }
 for box := range sa.videoCh {
  if sa.OnVideoBox != nil { sa.OnVideoBox(box) } else if sa.OnVideoFrame != nil { sa.OnVideoFrame(box.Data) }
 }
}
func (sa *Agent) ServeAudioStream() {
 if sa.audioCh == nil { return }
 for box := range sa.audioCh {
  if sa.OnAudioBox != nil { sa.OnAudioBox(box) } else if sa.OnAudioFrame != nil { sa.OnAudioFrame(box.Data) }
 }
}
