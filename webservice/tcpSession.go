package webservice

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"webscreen/sdriver"
	sagent "webscreen/streamAgent"
	"webscreen/sdriver/scrcpy"
	"sort"
	"strings"
)

// The screen protocol is carried exclusively by a same-origin WebSocket.
// Server messages: JSON metadata/errors; binary [kind, flags, uint64 PTS us, payload].
// Client binary messages are the existing Agent control packets, unmodified.
const (
	MAX_CLIENTS_PER_DEVICE = 4
	tcpVideo byte = 1
	tcpAudio byte = 2
	tcpFeedback byte = 3
	tcpHeaderSize = 10
	tcpWriteWait = 5 * time.Second
	tcpPongWait = 50 * time.Second
	tcpPingPeriod = 20 * time.Second
	tcpQueueSize = 64
	tcpQueueBytes = 16 * 1024 * 1024
)

var tcpUpgrader = websocket.Upgrader{
	HandshakeTimeout: 10 * time.Second,
	// Default CheckOrigin restricts browsers to the host serving the UI.
}

type tcpMessage struct {
	typ int
	data []byte
}

type tcpClient struct {
	conn *websocket.Conn
	queue chan tcpMessage
	done chan struct{}
	mu sync.Mutex
	bytes int
	closed bool
}

func newTCPClient(conn *websocket.Conn) *tcpClient {
	return &tcpClient{conn: conn, queue: make(chan tcpMessage, tcpQueueSize), done: make(chan struct{})}
}

func (c *tcpClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.closed {
		c.closed = true
		close(c.done)
		_ = c.conn.Close()
	}
}

// Do not let one slow browser stall the encoder or other subscribers. Do
// not drop dependent interframes: close the slow connection and let it rejoin.
func (c *tcpClient) enqueue(m tcpMessage) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	if c.bytes+len(m.data) <= tcpQueueBytes {
		select {
		case c.queue <- m:
			c.bytes += len(m.data)
			c.mu.Unlock()
			return true
		default:
		}
	}
	c.mu.Unlock()
	c.close()
	return false
}

func (c *tcpClient) writer() {
	defer c.close()
	ticker := time.NewTicker(tcpPingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case m := <-c.queue:
			c.mu.Lock()
			c.bytes -= len(m.data)
			c.mu.Unlock()
			_ = c.conn.SetWriteDeadline(time.Now().Add(tcpWriteWait))
			if err := c.conn.WriteMessage(m.typ, m.data); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(tcpWriteWait)); err != nil {
				return
			}
		}
	}
}

func tcpMedia(kind byte, box sdriver.AVBox) tcpMessage {
	data := make([]byte, tcpHeaderSize+len(box.Data))
	data[0] = kind
	if box.NoDuration {
		data[1] = 1
	}
	binary.BigEndian.PutUint64(data[2:10], box.PTS)
	copy(data[tcpHeaderSize:], box.Data)
	return tcpMessage{websocket.BinaryMessage, data}
}

func tcpJSON(v any) tcpMessage {
	data, _ := json.Marshal(v)
	return tcpMessage{websocket.TextMessage, data}
}

type tcpSession struct {
	agent *sagent.Agent
	configKey string
	meta tcpMessage
	mu sync.Mutex
	controlMu sync.Mutex
	clients map[*tcpClient]bool // false until a random-access video frame
	// Cache parameter sets individually for new subscribers and rotation.
	parameters map[byte][]byte
	audioConfig tcpMessage
	hevc bool
	done chan struct{}
	ended bool
}

// Return Annex-B NAL units without their start codes.
func tcpNALUs(data []byte) [][]byte {
	var units [][]byte
	start := -1
	for i := 0; i+2 < len(data); {
		n := 0
		if data[i] == 0 && data[i+1] == 0 {
			if data[i+2] == 1 {
				n = 3
			} else if i+3 < len(data) && data[i+2] == 0 && data[i+3] == 1 {
				n = 4
			}
		}
		if n == 0 {
			i++
			continue
		}
		if start >= 0 && i > start {
			if i > start { units = append(units, data[start:i]) }
		}
		i += n
		start = i
	}
	if start >= 0 && start < len(data) {
		units = append(units, data[start:])
	}
	return units
}

func (s *tcpSession) video(box sdriver.AVBox) {
	if len(box.Data) == 0 { return }
	// The legacy RTP driver strips the leading start code on ordinary frames.
	// MSE muxers require a complete Annex-B stream.
	if !bytes.HasPrefix(box.Data, []byte{0, 0, 1}) && !bytes.HasPrefix(box.Data, []byte{0, 0, 0, 1}) {
		box.Data = append([]byte{0, 0, 0, 1}, box.Data...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended { return }
	key := false
	for _, nal := range tcpNALUs(box.Data) {
		typ := nal[0] & 31
		param := typ == 7 || typ == 8
		if s.hevc {
			typ = (nal[0] >> 1) & 63
			param = typ == 32 || typ == 33 || typ == 34
			key = key || (typ >= 16 && typ <= 21)
		} else {
			key = key || typ == 5
		}
		if param {
			s.parameters[typ] = append([]byte{0,0,0,1}, nal...)
		}
	}
	// Cached IDR packets may carry NoDuration; they are video frames, not
	// configuration-only packets, and must be delivered to the decoder.
	if key { box.NoDuration = false }
	m := tcpMedia(tcpVideo, box)
	for c, ready := range s.clients {
		if !ready {
			if !key { continue }
			var sets []byte
			order := []byte{7,8}
			if s.hevc { order = []byte{32,33,34} }
			for _, typ := range order { sets = append(sets, s.parameters[typ]...) }
			if len(sets) > 0 {
				if !c.enqueue(tcpMedia(tcpVideo, sdriver.AVBox{Data:sets, PTS:box.PTS, NoDuration:true})) { continue }
			}
			s.clients[c] = true
		}
		c.enqueue(m)
	}
}

func (s *tcpSession) audio(box sdriver.AVBox) {
	if len(box.Data) == 0 { return }
	m := tcpMedia(tcpAudio, box)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended { return }
	if box.NoDuration || bytes.HasPrefix(box.Data, []byte("OpusHead")) { s.audioConfig = m }
	for c := range s.clients { c.enqueue(m) }
}

func (s *tcpSession) feedback(data []byte) {
	box := sdriver.AVBox{Data:data}
	m := tcpMedia(tcpFeedback, box)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients { c.enqueue(m) }
}

func (s *tcpSession) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended { return }
	s.ended = true
	close(s.done)
	for c := range s.clients { c.close() }
}

func agentConfigKey(config sagent.AgentConfig) string {
	keys := make([]string, 0, len(config.DriverConfig))
	for k := range config.DriverConfig {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(config.DeviceType)
	sb.WriteString("|")
	sb.WriteString(config.DeviceID)
	sb.WriteString("|")
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(config.DriverConfig[k])
		sb.WriteString(";")
	}
	sb.WriteString(fmt.Sprintf("|av=%v|ts=%v", config.AVSync, config.UseLocalTimestamp))
	return sb.String()
}

func (wm *WebMaster) joinTCP(config sagent.AgentConfig, c *tcpClient) (string, *tcpSession, error) {
	if config.DeviceType != sagent.DEVICE_TYPE_ANDROID { return "", nil, fmt.Errorf("only local Android is supported") }
	config.DeviceID = scrcpy.LocalDeviceName()
	config.DeviceIP = "127.0.0.1"
	config.DevicePort = "0"
	if config.DriverConfig == nil { config.DriverConfig = map[string]string{} }
	codec := config.DriverConfig["video_codec"]
	if codec == "" { codec = "h264"; config.DriverConfig["video_codec"] = codec }
	if codec != "h264" && codec != "h265" {
		return "", nil, fmt.Errorf("unsupported TCP video codec %q", codec)
	}
	id := config.DeviceType + "_" + config.DeviceID + "_" + config.DeviceIP + "_" + config.DevicePort
	key := agentConfigKey(config)
	wm.tcpMu.Lock()
	defer wm.tcpMu.Unlock()
	if wm.tcpSessions == nil { wm.tcpSessions = make(map[string]*tcpSession) }
	s := wm.tcpSessions[id]
	if s != nil {
		s.mu.Lock()
		ended := s.ended
		s.mu.Unlock()
		if ended || !s.agent.Alive() || s.configKey != key {
			s.finish()
			s.agent.Close()
			delete(wm.tcpSessions,id)
			s = nil
		}
	}
	newSession := s == nil
	if newSession {
		agent := sagent.New(config)
		if err := agent.InitDriver(); err != nil { return "", nil, err }
		s = &tcpSession{agent:agent,configKey:key,clients:make(map[*tcpClient]bool),parameters:make(map[byte][]byte),hevc:codec=="h265",done:make(chan struct{})}
		s.meta = tcpJSON(map[string]any{"status":"ok","stage":"tcp_metainfo","capabilities":agent.Capabilities(),"media_meta":agent.GetMediaMeta()})
		agent.OnVideoBox = s.video
		agent.OnAudioBox = s.audio
		agent.OnVideoEnd = s.finish
		wm.tcpSessions[id] = s
	}
	s.mu.Lock()
	if s.ended || len(s.clients) >= MAX_CLIENTS_PER_DEVICE {
		s.mu.Unlock()
		return "",nil,fmt.Errorf("TCP session unavailable or too many subscribers")
	}
	c.enqueue(s.meta)
	if len(s.audioConfig.data)>0 { c.enqueue(s.audioConfig) }
	s.clients[c] = false
	s.mu.Unlock()
	if newSession {
		go func() {
			for event := range s.agent.FeedbackEventsUntil(s.done) {
				s.feedback(event)
			}
		}()
		if err := s.agent.StartChecked(); err != nil {
			s.finish()
			s.agent.Close()
			delete(wm.tcpSessions,id)
			return "",nil,err
		}
	} else {
		s.controlMu.Lock()
		s.agent.PLIRequest()
		s.controlMu.Unlock()
	}
	return id,s,nil
}

func (wm *WebMaster) leaveTCP(id, deviceID string, s *tcpSession, c *tcpClient) {
	c.close()
	wm.tcpMu.Lock()
	defer wm.tcpMu.Unlock()
	s.mu.Lock()
	delete(s.clients,c)
	empty := len(s.clients)==0
	s.mu.Unlock()
	if empty && wm.tcpSessions[id]==s {
		s.finish()
		s.agent.Close()
		delete(wm.tcpSessions,id)
	}
}

func (wm *WebMaster) handleScreenTCP(ctx *gin.Context) {
	conn, err := tcpUpgrader.Upgrade(ctx.Writer,ctx.Request,nil)
	if err != nil { return }
	defer conn.Close()
	conn.SetReadLimit(1024*1024)
	_ = conn.SetReadDeadline(time.Now().Add(15*time.Second))
	var config sagent.AgentConfig
	if err := conn.ReadJSON(&config); err != nil { return }
	c := newTCPClient(conn)
	// Start the writer only after joining; metadata is queued before media.
	id,s,err := wm.joinTCP(config,c)
	if err != nil {
		_ = conn.SetWriteDeadline(time.Now().Add(tcpWriteWait))
		_ = conn.WriteJSON(map[string]any{"status":"error","message":err.Error(),"stage":"tcp_init"})
		return
	}
	defer wm.leaveTCP(id,config.DeviceID,s,c)
	go c.writer()
	_ = conn.SetReadDeadline(time.Now().Add(tcpPongWait))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(tcpPongWait)) })
	for {
		typ,data,err := conn.ReadMessage()
		if err != nil { return }
		if typ != websocket.BinaryMessage || len(data)==0 { continue }
		s.controlMu.Lock()
		if data[0]==0x63 {
			s.agent.PLIRequest()
		} else if err := s.agent.HandleEvent(data); err != nil {
			log.Printf("[tcp] control event rejected: %v",err)
		}
		s.controlMu.Unlock()
	}
}
