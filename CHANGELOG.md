# Changelog

## 1.3.0 — Android-only TCP (unreleased)

- Pure TCP WebSocket transport, JMuxer/MSE video and WebM/Opus audio.
- Retain user-tested TCP baseline low-latency buffering.
- Restrict deployment to the local rooted Android device; remove WebRTC, external ADB management, Linux desktop, Docker and fnOS paths.
- Build only Android arm64; check that legacy dependencies are absent.
- Retain package ID, port, foreground service, boot startup and Android controls.
- Fresh CI and device regression testing pending. APK size savings are not claimed until measured.
