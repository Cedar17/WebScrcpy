package sagent

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
