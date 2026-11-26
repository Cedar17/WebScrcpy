package webservice

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

)

// 受控端背光控制（隐私保护：屏幕黑、但不锁屏、串流与触控照常）。
//
// 为什么不按电源键：KEYCODE_POWER 会让设备熄屏并**锁定**，恢复时需要解锁，
// 与"串流仍可操作"的目标冲突。这里只把 backlight 的 brightness 写 0：
// 面板停止发光，设备保持唤醒状态，触摸/按键/串流全部正常。
//
// 关闭前会记录每个背光节点的原值与 max，恢复到文件里；恢复优先写回原值，
// 原值不可用时退回 max_brightness。
const backlightOffScript = `
set -e
prev=/data/local/tmp/webscreen_backlight_prev
: > "$prev"
for d in /sys/class/backlight/*/; do
  f="${d}brightness"
  [ -w "$f" ] || continue
  cur=$(cat "$f" 2>/dev/null || echo 0)
  max=$(cat "${d}max_brightness" 2>/dev/null || echo 0)
  [ "$cur" -gt 0 ] 2>/dev/null || continue
  echo "$d $cur $max" >> "$prev"
  echo 0 > "$f"
done
echo WEBSREEN_BACKLIGHT_OK
`

const backlightOnScript = `
set -e
prev=/data/local/tmp/webscreen_backlight_prev
if [ -s "$prev" ]; then
  # 每行格式： <目录> <原亮度> <最大亮度>；优先写回原亮度，缺失才用最大值。
  # 注意用 set -- 显式取字段，避免 read 在字段数不符时把值错位。
  while read -r line; do
    set -- $line
    d=$1; cur=${2:-}; max=${3:-}
    f="${d}brightness"
    [ -w "$f" ] || continue
    v=$cur
    case "$v" in ''|*[!0-9]*) v= ;;
    esac
    if [ -z "$v" ]; then
      v=$max
      case "$v" in ''|*[!0-9]*) v=255 ;;
      esac
    fi
    echo "$v" > "$f"
  done < "$prev"
  : > "$prev"
else
  for d in /sys/class/backlight/*/; do
    f="${d}brightness"
    [ -w "$f" ] || continue
    max=$(cat "${d}max_brightness" 2>/dev/null || echo 0)
    [ "$max" -gt 0 ] 2>/dev/null && echo "$max" > "$f"
  done
fi
echo WEBSREEN_BACKLIGHT_OK
`

type backlightRequest struct {
	Off    *bool  `json:"off"`
}

// Execute the existing local backlight script only on this Android device.
func runDeviceShell(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	encoded := base64.StdEncoding.EncodeToString([]byte(script))
	wrapped := fmt.Sprintf("echo %s | base64 -d | sh", encoded)
	out, err := exec.CommandContext(ctx, "/system/bin/su", "-c", wrapped).CombinedOutput()
	return string(out), err
}

func (wm *WebMaster) handleBacklight(c *gin.Context) {
	var req backlightRequest
	if err := c.BindJSON(&req); err != nil || req.Off == nil {
		c.JSON(http.StatusBadRequest, gin.H{"result": "error", "message": "invalid request"})
		return
	}

	script := backlightOnScript
	if *req.Off {
		script = backlightOffScript
	}

	out, err := runDeviceShell(script)
	trimmed := strings.TrimSpace(out)
	if err != nil || !strings.Contains(trimmed, "WEBSREEN_BACKLIGHT_OK") {
		msg := trimmed
		if err != nil {
			msg = trimmed + " " + err.Error()
		}
		log.Printf("[backlight] failed off=%v: %s", *req.Off, msg)
		c.JSON(http.StatusInternalServerError, gin.H{"result": "error", "message": msg})
		return
	}
	log.Printf("[backlight] off=%v ok", *req.Off)
	c.JSON(http.StatusOK, gin.H{"result": "success", "off": *req.Off})
}
