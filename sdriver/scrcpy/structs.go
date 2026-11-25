package scrcpy

type ScrcpyFrameHeader struct {
	IsConfig   bool
	IsKeyFrame bool
	PTS        uint64
	Size       uint32
}
