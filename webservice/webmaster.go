package webservice

import (
	"io/fs"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

)

type WebMaster struct {
	// WSConns []*websocket.Conn

	tcpMu sync.Mutex
	tcpSessions map[string]*tcpSession
	// ScreenSessions map[string]ScreenSession

	pin                  string
	UnlockAttemptRecords map[string]UnlockAttemptRecord
	jwtSecret            []byte

	router *gin.Engine
	staticFS            fs.FS
}

func New(staticFS fs.FS) *WebMaster {
	wm := &WebMaster{
		// ScreenSessions:       make(map[string]ScreenSession),
		staticFS:             staticFS,
		UnlockAttemptRecords: make(map[string]UnlockAttemptRecord),
		// TCP-only browser transport: do not initialize the legacy RTC manager.
	}
	wm.jwtSecret = []byte(time.Now().String())
	return wm
}

func Default(staticFS fs.FS) *WebMaster {
	return New(staticFS)
}

func (wm *WebMaster) setRouter() {
	// gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

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
		// api.GET("/generalConfigDescription", wm.handleGeneralConfigDescription)

		// api.POST("/device/discovery", wm.handleListDevicesDiscoveried)
		// api.POST("/setPIN", wm.handleSetPIN)

		// 受控端背光（隐私保护：屏幕黑，但不锁屏，串流照常）
		api.POST("/screen/backlight", wm.handleBacklight)
	}

	wm.router = r
}

func (wm *WebMaster) SetPIN(pin string) {
	// Set the PIN for web access
	// This is a placeholder implementation
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

func (wm *WebMaster) Close() {
	// for k, v := range maps.All(wm.ScreenSessions) {
	// 	log.Printf("closing session %v", k)
	// 	v.Close()
	// }
}
