# Changelog

## 1.4.0 — Cleanup (unreleased)

- Remove unused OCR translation and empty command script, obsolete transport callbacks and unsupported driver constants.
- Drop stale Docker/fnOS build ignore entries and legacy debug configuration.
- Remove unused WebRTC structs, driver stubs and obsolete device-ignore UI code/translations.
- Prune unused Android resources, old screenshot, redundant config types and unused browser control declarations.
- Consolidate tiny source files and use the concrete Scrcpy driver directly, eliminating the single-function utils package and the unused generic driver abstraction.
- Group driver lifecycle, control and keyframe methods by responsibility; consolidate connection settings and screen presentation helpers.
- Remove unreachable TCP socket setup from the local Unix-socket driver, redundant wrappers and stale browser rendering/loading paths.
- Remove unused scrcpy protocol declarations, obsolete remote-device backlight identifiers, and a duplicate NAL boundary check.
- Remove unused Android permissions and the unreachable pre-unlock boot receiver branch; retain normal boot startup.
- Remove redundant driver configuration assignments and fix the Android-only build output suffix.
- Collapse the two legacy browser data-channel facades into one TCP control channel, remove the inert browser wake-lock script, and retain only consumed SPS display dimensions.
- Keep the existing Android lifecycle, TCP protocol, media buffering, controls, authentication and signing unchanged.

## 1.3.0 — Android-only TCP

- Pure TCP WebSocket transport, JMuxer/MSE video and WebM/Opus audio.
- Retain user-tested TCP baseline low-latency buffering.
- Restrict deployment to the local rooted Android device; remove WebRTC, external ADB management, Linux desktop, Docker and fnOS paths.
- Build only Android arm64; check that legacy dependencies are absent.
- Retain package ID, port, foreground service, boot startup and Android controls.
- Fresh CI and device regression testing pending. APK size savings are not claimed until measured.
