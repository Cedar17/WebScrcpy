package webservice

import (
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"webscreen/sdriver/scrcpy"
	sagent "webscreen/streamAgent"

	"github.com/gin-gonic/gin"

)

type WebMaster struct {

	tcpMu sync.Mutex
	tcpSessions map[string]*tcpSession

	pin                  string
	UnlockAttemptRecords map[string]UnlockAttemptRecord
	jwtSecret            []byte

	router *gin.Engine
	staticFS            fs.FS
}

func New(staticFS fs.FS) *WebMaster {
	wm := &WebMaster{

		staticFS:             staticFS,
		UnlockAttemptRecords: make(map[string]UnlockAttemptRecord),

	}
	wm.jwtSecret = []byte(time.Now().String())
	return wm
}

func (wm *WebMaster) setRouter() {
	r := gin.New()
	// Periodic local readiness checks must not flood the Android UI log.
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{SkipPaths: []string{"/healthz"}}), gin.Recovery())

	// 前端资源是 //go:embed 进二进制的：升级后浏览器若沿用缓存的旧 HTML/JS，
	// 会出现"按钮在但点了没反应"（旧页面没有新脚本标签）这类极难排查的现象。
	// 因此统一禁止缓存，保证升级后刷新即生效。
	r.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	})

	subFS, _ := fs.Sub(wm.staticFS, "static")
	r.StaticFS("/static", http.FS(subFS))

	r.GET("/unlock", func(ctx *gin.Context) {
		ctx.FileFromFS("unlock.html", http.FS(wm.staticFS))
	})
	r.POST("/api/unlock", wm.handleUnlock)
	// Non-sensitive readiness marker used by the Android service, even with PIN enabled.
	r.GET("/healthz", func(c *gin.Context) { c.String(200, "WebScrcpy ready") })

	r.GET("/", func(ctx *gin.Context) {
		ctx.Redirect(302, "/console")
	})
	if wm.pin != "" {
		log.Println("Enable PIN middleware")
		r.Use(wm.HybridAuthMiddleware())
	}
	screen := r.Group("/screen")
	{
		screen.GET("/:id", func(ctx *gin.Context) {
			ctx.FileFromFS("screen.html", http.FS(wm.staticFS))
		})
		screen.GET("/ws", wm.handleScreenTCP)
	}

	r.GET("/console", func(c *gin.Context) {
		c.FileFromFS("console.html", http.FS(wm.staticFS))
	})
	api := r.Group("/api")
	{
		api.GET("/device/list", wm.handleListDevices)
		api.GET("/device/configDescription", wm.handleDeviceConfigDescription)

		// 受控端背光（隐私保护：屏幕黑，但不锁屏，串流照常）
		api.POST("/screen/backlight", wm.handleBacklight)
	}

	wm.router = r
}

func (wm *WebMaster) SetPIN(pin string) {
	// Set the PIN for web access
	log.Printf("PIN authentication enabled: %t", pin != "")
	wm.pin = pin
}

func (wm *WebMaster) Serve(host, port string) {
	wm.setRouter()
	err := wm.router.Run(host + ":" + port)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

type DeviceInfo struct {
	Type     string `json:"device_type"`
	DeviceID string `json:"device_id"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Status   string `json:"status"`
}

func (wm *WebMaster) handleListDevices(c *gin.Context) {
 c.JSON(200, gin.H{"devices": []DeviceInfo{{
 Type: sagent.DEVICE_TYPE_ANDROID, DeviceID: scrcpy.LocalDeviceName(),
 IP: "127.0.0.1", Port: 0, Status: "connected",
 }}})
}
func (wm *WebMaster) handleDeviceConfigDescription(c *gin.Context) {
 switch c.Query("device_type") {
 case sagent.DEVICE_TYPE_ANDROID: c.JSON(200, scrcpy.ConfigDescription())
 case "universal": c.JSON(200, sagent.ConfigDescription())
 default: c.JSON(400, gin.H{"error": "Only local Android is supported"})
 }
}
