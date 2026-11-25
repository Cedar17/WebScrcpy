package sdriver

type SDriver interface {
	GetReceivers() (<-chan AVBox, <-chan AVBox, chan Event)
	SendEvent(event Event) error

	Start() error

	RequestIDR(firstFrame bool)
	Capabilities() DriverCaps
	MediaMeta() MediaMeta
	Stop()
}
