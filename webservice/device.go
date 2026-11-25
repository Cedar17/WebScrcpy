package webservice

type DeviceInfo struct {
	Type     string `json:"device_type"`
	DeviceID string `json:"device_id"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Status   string `json:"status"`
}
