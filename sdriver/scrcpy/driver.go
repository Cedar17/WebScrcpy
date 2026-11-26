package scrcpy

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"webscreen/sdriver"
	"webscreen/sdriver/comm"
)

type ScrcpyDriver struct {
	VideoChan   chan sdriver.AVBox
	AudioChan   chan sdriver.AVBox
	ControlChan chan sdriver.Event

	// LinearBuffer 管理器
	videoBuffer *comm.LinearBuffer
	audioBuffer *comm.LinearBuffer

	mediaMeta  sdriver.MediaMeta
	deviceName string

	videoConn   net.Conn
	audioConn   net.Conn
	controlConn net.Conn

	capabilities sdriver.DriverCaps

	ctx       context.Context
	cancel    context.CancelFunc
	scid      string

	// handle of the locally started scrcpy server
	localServerCmd *exec.Cmd

	// dead becomes true as soon as the scrcpy session is gone, so that a new
	// session rebuilds the pipeline instead of reusing a dead one.
	dead atomic.Bool

	cacheMutex         sync.RWMutex
	LastVPS            []byte
	LastSPS            []byte
	LastPPS            []byte
	LastIDR            []byte
	LastPTS            uint64
	LastIDRRequestTime time.Time
}

// 一个ScrcpyDriver对应一个scrcpy实例，通过本地端口建立三个连接：视频、音频、控制
func New(config map[string]string) (*ScrcpyDriver, error) {
	var err error
	da := &ScrcpyDriver{
		VideoChan:   make(chan sdriver.AVBox, 10),
		AudioChan:   make(chan sdriver.AVBox, 10),
		ControlChan: make(chan sdriver.Event, 10),

		videoBuffer: comm.NewLinearBuffer(0),
		audioBuffer: comm.NewLinearBuffer(4 * 1024 * 1024), // 4MB 音频缓冲区

		scid: "00000000",

		capabilities: sdriver.DriverCaps{
			IsAndroid: true,
		},
	}
	da.ctx, da.cancel = context.WithCancel(context.Background())

	// scrcpy defaults video/audio/control to true. The web UI omits them
	// entirely for a device that was never configured, which would leave the
	// server waiting for stream sockets that we never open, so apply the same
	// defaults here and always pass every flag explicitly.
	if config["video"] == "" {
		config["video"] = "true"
	}
	if config["audio"] == "" {
		config["audio"] = "true"
	}
	if config["control"] == "" {
		config["control"] = "true"
	}
	if config["video_codec"] == "" {
		config["video_codec"] = "h264"
	}

	scrcpyServerPath, err := writeLocalScrcpyServer(scrcpyServerData)
	if err != nil { return nil, err }
	if !LocalSupportsOpusAudio() {
		config["audio"] = "false"
		log.Println("[scrcpy] Device does not support Opus audio encoding, disabling audio.")
		da.ControlChan <- sdriver.TextMsgEvent{Msg: "[scrcpy] Device does not support Opus audio encoding, disabling audio."}
	}

	streams := 1
	if config["audio"] == "true" { streams++ }
	if config["control"] == "true" { streams++ }
	listener := newAbstractListener(fmt.Sprintf("scrcpy_%s", da.scid), LOCAL_CONNECT_TIMEOUT, streams)
	// 失败时必须收尾：listener 不关会让 tcp 端口 / 抽象 socket 一直被占，
	// reverse 隧道与已建立的连接也会残留下来，下一次会话就会撞端口或连到残留的
	// server，在读取设备元数据时再次拿到 EOF —— 这正是用户偶尔看到的
	// "服务器错误: failed to ensure agent: EOF" 的来源。
	// 注意 New() 原有的失败分支只处理了 Accept 失败；readDeviceMeta 失败那条
	// 路径此前是直接 return 的，漏掉了全部清理。
	setupOK := false
	defer func() {
		if setupOK {
			return
		}
		listener.Close()
		for _, c := range []net.Conn{da.videoConn, da.audioConn, da.controlConn} {
			if c != nil {
				c.Close()
			}
		}
		da.stopLocalServer()
	}()

	log.Printf("[scrcpy] driver config: %v", config)
	max_size, err := strconv.Atoi(config["max_size"])
	if err != nil {
		max_size = 3840
	}
	max_fps, err := strconv.Atoi(config["max_fps"])
	if err != nil {
		max_fps = 120
	}
	video_bit_rate_str, ok := config["video_bit_rate"]
	if !ok || video_bit_rate_str == "" {
		video_bit_rate_str = "4M"
		config["video_bit_rate"] = "4M" // 默认 4 Mbps
	}
	video_bit_rate, err := parseBitrate(video_bit_rate_str)
	if err != nil {
		return nil, fmt.Errorf("invalid video bit rate: %v", err)
	}
	if video_bit_rate <= 0 {
		// An empty/zero bit rate would be forwarded to scrcpy and the encoder
		// would not produce any frame (this is what the web UI sends when the
		// device was never configured), so fall back to a sane default.
		log.Printf("[scrcpy] invalid video bit rate %q, falling back to 4M", video_bit_rate_str)
		video_bit_rate = 4_000_000
		config["video_bit_rate"] = "4M"
	}
	// TCP has no SDP negotiation; retain explicitly selected encoder options.
	video_codec_options := config["video_codec_options"]
	if video_codec_options != "" {
		log.Printf("Using user-provided video codec options: %s", video_codec_options)
	}

	options := map[string]string{
		"CLASSPATH":           scrcpyServerPath,
		"Version":             SCRCPY_VERSION,
		"scid":                da.scid,
		"max_size":            strconv.Itoa(max_size),
		"max_fps":             strconv.Itoa(max_fps),
		"video":               config["video"],
		"video_bit_rate":      strconv.Itoa(video_bit_rate),
		"video_codec":         config["video_codec"],
		"video_codec_options": video_codec_options, // bitrate-mode=2 to enable CBR
		"audio":               config["audio"],
		"control":   config["control"],
		"cleanup":   "true",
		"log_level": "info",

		"video_encoder": config["video_encoder"],
	}
	if config["video_encoder"] != "" {
		log.Printf("Using user-specified video encoder: %s", config["video_encoder"])
	}
	if config["no_video_codec_options"] == "true" {
		delete(options, "video_codec_options")
		log.Println("User requested to disable video codec options, ignoring all codec options.")
	}
	if config["new_display"] == "true" {
		options["new_display"] = fmt.Sprintf("%s/%d", config["resolution"], max_fps)
		log.Printf("Using new virtual display with resolution %s and max_fps %d", config["resolution"], max_fps)
	}

	options["tunnel_forward"] = "true"
	options["send_dummy_byte"] = "false"
	if err := da.startLocalScrcpyServer(options); err != nil {
		return nil, err
	}
	log.Println("Connecting to local scrcpy sockets")

	if options["video"] == "true" {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[scrcpy] Accept failed (可能是 scrcpy-server 启动失败): %v", err)
			return nil, fmt.Errorf("failed to accept connection from scrcpy-server: %v", err)
		}
		err = da.readDeviceMeta(conn)
		if err != nil {
			log.Println("Failed to read device metadata:", err)
			return nil, err
		}
		log.Printf("[scrcpy] Connected Device: %s", da.deviceName)

		da.assignConn(conn)
	}
	if options["audio"] == "true" {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[scrcpy] Accept failed (可能是 scrcpy-server 启动失败): %v", err)
			return nil, fmt.Errorf("failed to accept connection from scrcpy-server: %v", err)
		}
		da.assignConn(conn)
	}
	if options["control"] == "true" {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[scrcpy] Accept failed (可能是 scrcpy-server 启动失败): %v", err)
			return nil, fmt.Errorf("failed to accept connection from scrcpy-server: %v", err)
		}
		da.controlConn = conn
		da.capabilities.CanControl = true
		da.capabilities.CanUHID = true
		da.capabilities.CanClipboard = true
		log.Println("Scrcpy Control Connection Established")
	}

	listener.Close()

	setupOK = true
	return da, nil
}

// Alive reports whether the scrcpy session is still usable. A driver becomes
// dead when the scrcpy server exits or one of the stream connections breaks
// (e.g. the UHID controller error that kills the server), and must not be
// reused for a new session in that case.
func (da *ScrcpyDriver) Alive() bool {
	return !da.dead.Load()
}

// markDead flags the session as broken exactly once.
func (da *ScrcpyDriver) markDead(reason string) {
	if da.dead.CompareAndSwap(false, true) {
		log.Printf("[scrcpy] session is dead: %s", reason)
		// Non blocking: tell the connected browsers why the stream stopped.
		select {
		case da.ControlChan <- sdriver.TextMsgEvent{Msg: "[scrcpy] " + reason + "，请重新连接"}:
		default:
		}
	}
}

// Please Ensure the input conn is not Control conn
func (da *ScrcpyDriver) assignConn(conn net.Conn) {
	codecID := readCodecID(conn)
	switch codecID {
	case "h264", "h265", "av1":
		da.videoConn = conn
		da.mediaMeta.VideoCodec = codecID
		da.capabilities.CanVideo = true
		log.Println("Scrcpy Video Connection Established")
	case "aac ", "opus":
		da.audioConn = conn
		da.mediaMeta.AudioCodec = codecID
		da.capabilities.CanAudio = true
		log.Println("Audio Connection Established")
	}
}

func (da *ScrcpyDriver) readDeviceMeta(conn net.Conn) error {
	// 1. Device Name (64 bytes)
	nameBuf := make([]byte, 64)
	_, err := io.ReadFull(conn, nameBuf)
	if err != nil {
		return err
	}
	log.Println("read nameBuf from scrcpy: ", string(nameBuf))
	da.deviceName = string(nameBuf)
	return nil
}

func (da *ScrcpyDriver) updateVideoMetaFromSPS(sps []byte, codec string) {
	if da.LastSPS != nil && bytes.Equal(da.LastSPS, sps) {
		return
	}
	var spsInfo comm.SPSInfo
	var err error
	switch codec {
	case "h264":
		spsInfo, err = comm.ParseSPS_H264(sps)
	case "h265":
		spsInfo, err = comm.ParseSPS_H265(sps)
	default:
		log.Println("Unknown codec type for SPS parsing:", codec)
		return
	}

	if err != nil {
		log.Println("Failed to parse SPS for video meta update:", err)
		return
	}
	da.updateSize(spsInfo.Width, spsInfo.Height)
}

func (da *ScrcpyDriver) updateSize(width, height uint32) {
	da.mediaMeta.Width = width
	da.mediaMeta.Height = height
	log.Printf("[scrcpy] Updated Video Meta from session packet: Width=%d, Height=%d", da.mediaMeta.Width, da.mediaMeta.Height)
}

func readScrcpyFrameHeader(headerBuf []byte, header *ScrcpyFrameHeader) {

	ptsAndFlags := binary.BigEndian.Uint64(headerBuf[0:8])
	packetSize := binary.BigEndian.Uint32(headerBuf[8:12])

	// 提取标志位
	isKeyFrame := (headerBuf[0] & 0x20) != 0

	// 提取PTS (低61位)
	pts := ptsAndFlags & 0x1FFFFFFFFFFFFFFF
	header.IsKeyFrame = isKeyFrame
	header.PTS = pts
	header.Size = packetSize
}

func readCodecID(conn net.Conn) string {
	// Codec ID (4 bytes)
	codecBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, codecBuf); err != nil {
		log.Println("Failed to read codec ID:", err)
		return ""
	}

	return string(codecBuf)
}

func createCopy(src []byte, context string) []byte {
	if len(src) == 0 {
		log.Printf("createCopy called with src length: %d, context: %s", len(src), context)
		log.Println("createCopy called with empty src")
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func parseBitrate(bitrateStr string) (int, error) {
	var bitrate int
	var multiplier int = 1

	if len(bitrateStr) == 0 {
		return 0, nil
	}

	lastChar := bitrateStr[len(bitrateStr)-1]
	switch lastChar {
	case 'K', 'k':
		multiplier = 1000
		bitrateStr = bitrateStr[:len(bitrateStr)-1]
	case 'M', 'm':
		multiplier = 1000000
		bitrateStr = bitrateStr[:len(bitrateStr)-1]
	case 'G', 'g':
		multiplier = 1000000000
		bitrateStr = bitrateStr[:len(bitrateStr)-1]
	}

	var err error
	bitrate, err = strconv.Atoi(bitrateStr)
	if err != nil {
		return 0, fmt.Errorf("invalid bitrate format: %s", bitrateStr)
	}

	return bitrate * multiplier, nil
}

func (sd *ScrcpyDriver) GetReceivers() (<-chan sdriver.AVBox, <-chan sdriver.AVBox, chan sdriver.Event) {
	return sd.VideoChan, sd.AudioChan, sd.ControlChan
}

func (sd *ScrcpyDriver) Start() {
	log.Println("ScrcpyDriver: Start called")
	if sd.videoConn != nil {
		go sd.convertVideoFrame()
	}
	if sd.audioConn != nil {
		go sd.convertAudioFrame()
	}
	if sd.controlConn != nil {
		go sd.transferControlMsg()
	}
}

func (sd *ScrcpyDriver) Capabilities() sdriver.DriverCaps {
	return sd.capabilities
}

func (sd *ScrcpyDriver) MediaMeta() sdriver.MediaMeta {
	return sd.mediaMeta
}

func (sd *ScrcpyDriver) Stop() {
	if sd.videoConn != nil {
		sd.videoConn.Close()
	}
	if sd.audioConn != nil {
		sd.audioConn.Close()
	}
	if sd.controlConn != nil {
		sd.controlConn.Close()
	}
	sd.stopLocalServer()
	sd.cancel()
	sd.markDead("会话已停止")
}
