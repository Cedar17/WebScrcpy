# Changelog

## 1.4.0 — Cleanup (unreleased)

- Remove unused OCR translation and empty command script, obsolete transport callbacks and unsupported driver constants.
- Drop stale Docker/fnOS build ignore entries and legacy debug configuration.
- Remove unused WebRTC structs, driver stubs and obsolete device-ignore UI code/translations.
- Keep the existing Android lifecycle, TCP protocol, media buffering, controls, authentication and signing unchanged.

## 1.3.0 — Android-only TCP

- Pure TCP WebSocket transport, JMuxer/MSE video and WebM/Opus audio.
- Retain user-tested TCP baseline low-latency buffering.
- Restrict deployment to the local rooted Android device; remove WebRTC, external ADB management, Linux desktop, Docker and fnOS paths.
- Build only Android arm64; check that legacy dependencies are absent.
- Retain package ID, port, foreground service, boot startup and Android controls.
- Fresh CI and device regression testing pending. APK size savings are not claimed until measured.
