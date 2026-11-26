package webservice

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type CustomClaims struct {
	Role string `json:"role"`
	jwt.RegisteredClaims
}

func (wm *WebMaster) GenerateToken() (string, error) {
	// 设置 Token 有效期
	expirationTime := time.Now().Add(2 * time.Hour)

	claims := &CustomClaims{
		Role: "admin", // 你可以在这里存用户ID或其他信息
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			Issuer:    "webscreen",
		},
	}

	// 使用 HS256 算法进行签名
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(wm.jwtSecret)
	return tokenString, err
}

// ---------------------------------------------------------
// 中间件：JWT 认证拦截器
// ---------------------------------------------------------
func (wm *WebMaster) HybridAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// 1. 优先尝试从 Cookie 取
		tokenString, _ = c.Cookie("auth_token")

		// 2. 如果 Cookie 没有，尝试从 Header 取
		if tokenString == "" {
			authHeader := c.GetHeader("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				tokenString = authHeader[7:]
			}
		}

		// 3. 都没有，或者验证失败
		if tokenString == "" || !wm.validateToken(tokenString) { // validateToken 是你自己封装的验证逻辑

			// 判断请求类型：如果是请求网页(Accept text/html)，跳转；如果是 API (Accept application/json)，返回 401
			accept := c.GetHeader("Accept")
			if strings.Contains(accept, "text/html") {
				c.Redirect(http.StatusFound, "/unlock")
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			}
			return
		}

		c.Next()
	}
}
func (wm *WebMaster) validateToken(tokenString string) bool {
	claims := &CustomClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return wm.jwtSecret, nil
	})

	if err != nil || !token.Valid {
		return false
	}

	return true

}

type UnlockAttemptRecord struct {
	Attempts  int
	IsLocked  bool
	LockUntil time.Time
}

// User need to enter correct PIN to unlock the web access
func (wm *WebMaster) handleUnlock(c *gin.Context) {
	var req struct {
		PIN string `json:"pin"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"result": "error", "message": "Invalid request"})
		return
	}
	sIP := c.ClientIP()
	var record UnlockAttemptRecord
	record, ok := wm.UnlockAttemptRecords[sIP]
	if ok {
		if record.IsLocked {
			if time.Now().Before(record.LockUntil) {
				c.JSON(200, gin.H{"result": "failed", "message": "Too many attempts, please try again later", "leftTries": 0, "lockUntil": record.LockUntil})
				return
			} else {
				// Unlock the record
				record.IsLocked = false
				record.Attempts = 0
				wm.UnlockAttemptRecords[sIP] = record
			}
		}
		if record.Attempts >= 5 {
			record.IsLocked = true
			record.LockUntil = time.Now().Add(10 * time.Minute)
			wm.UnlockAttemptRecords[sIP] = record

			c.JSON(200, gin.H{"result": "failed", "message": "Too many attempts, please try again later", "leftTries": 0, "lockUntil": record.LockUntil})
			return
		}
	} else {
		record = UnlockAttemptRecord{
			Attempts:  0,
			IsLocked:  false,
			LockUntil: time.Time{},
		}
		wm.UnlockAttemptRecords[sIP] = record
	}

	if req.PIN != wm.pin {
		c.JSON(200, gin.H{"result": "failed", "message": "Incorrect PIN", "leftTries": 5 - record.Attempts})
		record.Attempts++
		wm.UnlockAttemptRecords[sIP] = record
		return
	}

	token, err := wm.GenerateToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Token Generation Failed"})
		return
	}
	c.SetCookie("auth_token", token, 3600*2, "/", "", false, true)
	c.JSON(200, gin.H{"result": "success", "message": "Unlocked", "token": token})
	delete(wm.UnlockAttemptRecords, sIP)
}
