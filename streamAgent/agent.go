package sagent

import (
	"fmt"
	"iter"
	"log"
	"time"
	"webscreen/sdriver"
	"webscreen/sdriver/scrcpy"
)

const DEVICE_TYPE_ANDROID = "android"

type AgentConfig struct {
	DeviceType        string            `json:"device_type"`
	DeviceID          string            `json:"device_id"`
	DeviceIP          string            `json:"device_ip"`
	DevicePort        string            `json:"device_port"`
	AVSync            bool              `json:"av_sync"`
	UseLocalTimestamp bool              `json:"use_local_timestamp"`
	DriverConfig      map[string]string `json:"driver_config"`
}

func ConfigDescription() []sdriver.ConfigParamDescription {

	return []sdriver.ConfigParamDescription{
		{
			Name:        "av_sync",
			Type:        "boolean",
			Required:    true,
			Default:     false,
			Description: "Enable A/V sync. Useful when watching videos.",
		},
		{
			Name:        "use_local_timestamp",
			Type:        "boolean",
			Required:    true,
			Default:     true,
			Description: "Use local timestamp instead of device timestamp. This may reduce latency but the video may be less smooth.",
		},

	}
}

type Agent struct {
	driver *scrcpy.ScrcpyDriver
	config AgentConfig
	// chan
	videoCh   <-chan sdriver.AVBox
	audioCh   <-chan sdriver.AVBox
	controlCh chan sdriver.Event

	// Preserve scrcpy timestamps and codec configuration packets for TCP transport.
	OnVideoBox func(sdriver.AVBox)
	OnAudioBox func(sdriver.AVBox)
	OnVideoEnd func()
}

// ========================
// SAgent 负责初始化driver并接受来自sdriver的数据，并处理来自前端的控制命令。
// 提供一系列Hook
// ========================
func New(config AgentConfig) *Agent {
	return &Agent{config: config}
}

// driverInitAttempts 是 scrcpy 会话启动的最大尝试次数（首次 + 重试）。
const driverInitAttempts = 3

func (sa *Agent) InitDriver() error {
	if sa.config.DriverConfig == nil {
		sa.config.DriverConfig = make(map[string]string)
	}
	if sa.config.DeviceType != DEVICE_TYPE_ANDROID {
		log.Printf("Unsupported device type: %s", sa.config.DeviceType)
		return fmt.Errorf("unsupported device type: %s", sa.config.DeviceType)
	}
	// 初始化 Android Driver（失败有限重试）
	//
	// scrcpy 会话启动是一次性握手：设备端 scrcpy-server 起得慢、上一会话的
	// socket/端口尚未释放等，都会让读取设备元数据时直接拿到 EOF，用户看到的
	// 就是“服务器错误: failed to ensure agent: EOF”。这类失败多为瞬时且可恢复，
	// 而 scrcpy.New 的失败路径会释放 listener/隧道/连接，因此重试是安全的。
	var lastErr error
	for attempt := 1; attempt <= driverInitAttempts; attempt++ {
		androidDriver, err := scrcpy.New(sa.config.DriverConfig)
		if err == nil {
			sa.driver = androidDriver
			if attempt > 1 {
				log.Printf("Android driver initialized on attempt %d/%d", attempt, driverInitAttempts)
			}
			break
		}
		lastErr = err
		log.Printf("Failed to initialize Android driver (attempt %d/%d): %v", attempt, driverInitAttempts, err)
		if attempt < driverInitAttempts {
			time.Sleep(time.Duration(attempt) * 500 * time.Millisecond)
		}
	}
	if sa.driver == nil {
		return lastErr
	}

	sa.videoCh, sa.audioCh, sa.controlCh = sa.driver.GetReceivers()
	return nil
}

func (sa *Agent) Close() {
	log.Printf("Closing agent for device %s", sa.config.DeviceID)
	if sa.driver != nil {
		sa.driver.Stop()
	}
}

// Alive reports whether the underlying driver session is still usable. It is
// used to drop a dead pipeline (crashed scrcpy server, broken socket) instead
// of reusing it for the next session.
func (sa *Agent) Alive() bool {
	if sa.driver == nil {
		return false
	}
	return sa.driver.Alive()
}

func (sa *Agent) GetMediaMeta() sdriver.MediaMeta {
	return sa.driver.MediaMeta()
}

func (sa *Agent) Capabilities() sdriver.DriverCaps {
	return sa.driver.Capabilities()
}

func (sa *Agent) Start() {
	sa.driver.Start()
	go sa.ServeVideoStream()
	go sa.ServeAudioStream()

	sa.driver.RequestIDR(true)
}

func (sa *Agent) PLIRequest() {
	sa.driver.RequestIDR(false)
}

func (sa *Agent) HandleEvent(raw []byte) error {
	if sa.driver == nil || !sa.driver.Capabilities().CanControl {
		return fmt.Errorf("driver does not support control events")
	}
	event, err := sa.parseEvent(raw)
	if err != nil {
		log.Printf("[agent] Failed to parse control event: %v", err)
		return err
	}
	return sa.driver.SendEvent(event)
}

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
