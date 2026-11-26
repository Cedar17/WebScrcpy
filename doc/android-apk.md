# WebScrcpy Android 使用说明

仅支持已 Root 的 Android 本机作为受控端。无需 PC、NAS、ADB 或 Termux，控制端仅需兼容 MSE 的浏览器。

## 安装与使用

1. 安装 arm64-v8a APK（Android 8+），开发版从 Actions 的 Build APK 下载 WebScreen-debug artifact。
2. 在 Magisk / KernelSU / APatch 中授予 Root 权限；静默拒绝时手动授权。
3. 设置 6 位 PIN，按需开启开机自启动，点击启动。
4. 浏览器访问显示的 HTTP 地址（默认 8079），选择本机并开始串流。

首次建议 H.264、30 FPS、4 Mbps。音频、H.265、黑屏和多客户端兼容性以设备及浏览器为准。

## 架构与测试

内嵌 scrcpy-server 5.0.1 经 app_process 在本机启动，通过抽象 Unix socket 与 Go 服务通信。浏览器通过同源 /screen/ws WebSocket 交换视频、音频和控制消息，采用 JMuxer/MSE 与 WebM/Opus。保留原有低延迟策略及 UHID 组设置。

1.3.0 TCP 基线已由用户确认可用；1.4.0 清理版仍需重新构建及真机回归：安装、启动、画面、触控、重连、旋转、音频、黑屏恢复、开机自启、PIN 及 VPN 访问。

HTTP/WS 默认不加密，PIN 不替代 TLS；仅在可信私网或安全 VPN 使用，不直接暴露公网。部分继承的 UI 样式和字体使用 CDN，尚不保证完全离线。

构建见 [README](../README.md)。内部包名 com.webscreen.app 保持不变。公开固定 CI 签名仅供测试；正式分发须使用私有签名，迁移签名需卸载旧包。

保留原作者署名、AGPL-3.0 及第三方许可证。
