package webservice
import (
 "webscreen/sdriver/scrcpy"
 sagent "webscreen/streamAgent"
 "github.com/gin-gonic/gin"
)
func (wm *WebMaster) handleListDevices(c *gin.Context) {
 c.JSON(200, gin.H{"devices": []DeviceInfo{{
 Type: sagent.DEVICE_TYPE_ANDROID, DeviceID: scrcpy.LocalDeviceName(),
 IP: "127.0.0.1", Port: 0, Status: "connected",
 }}})
}
func (wm *WebMaster) handleDeviceConfigDescription(c *gin.Context) {
 switch c.Query("device_type") {
 case sagent.DEVICE_TYPE_ANDROID: c.JSON(200, scrcpy.ConfigDescription(c.Query("device_id")))
 case "universal": c.JSON(200, sagent.ConfigDescription())
 default: c.JSON(400, gin.H{"error": "Only local Android is supported"})
 }
}
