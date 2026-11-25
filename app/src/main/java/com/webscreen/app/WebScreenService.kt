package com.webscreen.app

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.util.Log
import java.net.HttpURLConnection
import java.net.URL
import java.net.Proxy
import org.json.JSONObject
import java.util.concurrent.Executors
import android.os.SystemClock

/**
 * Runs the webscreen server (the Go binary shipped as libwebscreen.so) as root
 * in the background, so that closing the activity, closing the browser or
 * turning the screen off does not stop the stream.
 */
class WebScreenService : Service() {

    // Process ownership stays on one worker; log readers post exit events back to it.
    private val worker = Executors.newSingleThreadExecutor()
    private var process: Process? = null
    private var queuedStartRevision: Long? = null
    private var queuedStopRevision: Long? = null

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                val current = ServerState.status
                val revision = if (current.phase == ServerState.Phase.STOPPING) current.revision
                    else ServerState.requestStop()
                if (revision != null && queuedStopRevision != revision) {
                    queuedStopRevision = revision
                    worker.execute {
                        stopServer(revision)
                        stopForegroundCompat()
                        stopSelfResult(startId)
                    }
                } else if (revision == null) {
                    stopForegroundCompat()
                    stopSelfResult(startId)
                }
            }
            else -> {
                val current = ServerState.status
                val revision = if (current.phase == ServerState.Phase.STARTING) current.revision
                    else ServerState.requestStart()
                if (revision == null || queuedStartRevision == revision) return START_NOT_STICKY
                queuedStartRevision = revision
                // Android 15+（SDK 35+）的部分 ROM 会拒绝前台服务：
                // 实测一加 7 Pro（Android 16）上 startForeground 明明成功
                // （日志 "[app] 前台服务已启动（type=2）"），系统仍在 ~96ms 后
                // 以 "has died: prcp FGS" 杀掉整个进程，并伴随
                // "ForegroundServiceTypeLoggerModule: Logger should be tracking
                // FGS types correctly"。也就是说拒绝发生在 ROM 的 FGS 校验层，
                // 与我们声明哪种类型无关 —— 换类型无效。
                //
                // 因此在这些系统上干脆不提升前台服务，只跑普通服务。
                // webscreen 二进制是以 exec 启动的独立进程，服务随后被回收
                // 也不会中断串流；而普通服务不触发 FGS 校验，进程不会被杀。
                if (Build.VERSION.SDK_INT >= 35) {
                    ServerState.append("[app] Android 15+：跳过前台服务提升，改用普通服务（规避 ROM 的 FGS 校验）")
                } else {
                    try {
                        startForegroundCompat()
                    } catch (t: Throwable) {
                        ServerState.append("[app] 前台服务启动异常（已忽略，降级运行）: ${t.javaClass.simpleName}: ${t.message}")
                        Log.e(TAG, "startForegroundCompat failed, degrade to background service", t)
                    }
                }
                worker.execute {
                    try {
                        startServer(revision)
                    } catch (t: Throwable) {
                        fail(revision, "启动失败: ${t.javaClass.simpleName}: ${t.message}")
                        Log.e(TAG, "startServer failed", t)
                    }
                }
            }
        }
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        // The Go process is started with `exec`, so it survives the service only
        // if it was not asked to stop. Nothing to do here beyond bookkeeping.
        ServerState.append("[service] stopped")
        worker.shutdown()
        super.onDestroy()
    }

    // ------------------------------------------------------------------ server

    private fun startServer(revision: Long) {
        if (ServerState.status.revision != revision) return
        val prefs = Prefs(this)
        val port = prefs.port
        // An already-running instance can outlive the Android Service.
        if (isReady(port)) {
            ServerState.transition(revision, ServerState.Phase.RUNNING)
            updateNotification()
            return
        }

        val binary = RootShell.packagedBinary(this)
        if (!binary.isFile) {
            fail(revision, "APK 中缺少 WebScrcpy 二进制 (${binary.absolutePath})")
            return
        }

        // Do not launch a second instance over an existing live root process.
        if (detectRunning()) {
            // A detached root process may still be warming up (or survived an APK update).
            // Never launch a second instance or kill it just because the first probe failed.
            val deadline = SystemClock.elapsedRealtime() + 5000
            while (ServerState.status.revision == revision && SystemClock.elapsedRealtime() < deadline) {
                if (isReady(port)) {
                    ServerState.transition(revision, ServerState.Phase.RUNNING)
                    updateNotification()
                    return
                }
                Thread.sleep(200)
            }
            fail(revision, "检测到服务进程，但 HTTP 未就绪: " + lastProbeDetail)
            return
        }
        if (ServerState.status.revision != revision) return

        val remote = RootShell.REMOTE_BINARY
        val args = StringBuilder()
        args.append("'").append(remote).append("'")
        args.append(" -host 0.0.0.0")
        args.append(" -port ").append(port)
        if (prefs.pin.isNotEmpty()) {
            args.append(" -pin ").append(prefs.pin)
        }

        val command = listOf(
            "mkdir", "-p", RootShell.WORK_DIR, "&&",
            "cp", "-f", "'${binary.absolutePath}'", "'$remote'", "&&",
            "chmod", "755", "'$remote'", "&&",
            "exec", args.toString()
        ).joinToString(" ")

        ServerState.append("[app] Starting WebScrcpy on port $port")
        val started = ProcessBuilder(listOf(RootShell.suBinary(), "-c", command))
            .redirectErrorStream(true).start()
        process = started

        Thread {
            try {
                started.inputStream.bufferedReader().forEachLine { ServerState.append(it) }
            } catch (e: Exception) {
                ServerState.append("[app] log reader stopped: ${e.message}")
            }
            val code = try { started.waitFor() } catch (e: InterruptedException) { -1 }
            // The old process must never overwrite a later start/stop request.
            try {
                worker.execute {
                    if (process === started) {
                        process = null
                        // The su wrapper can exit while the detached root server survives.
                        // The activity reconciles actual HTTP readiness, not wrapper lifetime.
                    }
                    ServerState.append("[app] WebScrcpy exited with code $code")
                }
            } catch (_: java.util.concurrent.RejectedExecutionException) {
                // The Android service was destroyed; the activity reconciles HTTP readiness.
            }
        }.apply { isDaemon = true; start() }

        val deadline = SystemClock.elapsedRealtime() + 20000
        while (SystemClock.elapsedRealtime() < deadline && ServerState.status.revision == revision) {
            if (isReady(port)) {
                if (ServerState.transition(revision, ServerState.Phase.RUNNING)) {
                    ServerState.append("[app] WebScrcpy HTTP server ready")
                    updateNotification()
                }
                return
            }
            if (!started.isAlive) {
                process = null
                fail(revision, "服务进程提前退出 (exit=${started.exitValue()})")
                return
            }
            Thread.sleep(200)
        }

        // STOPPING owns cleanup when the user cancelled this start.
        if (ServerState.status.revision != revision) return
        // A failed readiness probe is not proof that the root process is dead.
        // Keep it available for late readiness or an explicit Stop.
        fail(revision, "服务未在 20 秒内完成 HTTP 探测: " + lastProbeDetail)
    }

    private fun stopServer(revision: Long) {
        try {
            process?.destroy()
            process = null
            RootShell.run(
                listOf(
                    "pkill", "-f", "[c]om.genymobile.scrcpy.Server", ";",
                    "pkill", "-f", "[w]ebscreen/webscreen"
                ),
                timeoutSeconds = 20
            )
            val port = Prefs(this).port
            val deadline = SystemClock.elapsedRealtime() + 5000
            while (isReady(port) && SystemClock.elapsedRealtime() < deadline) Thread.sleep(200)
            if (isReady(port) || detectRunning()) {
                fail(revision, "服务仍在运行，请查看 Root 权限和日志")
            } else {
                ServerState.transition(revision, ServerState.Phase.STOPPED)
                ServerState.append("[app] WebScrcpy stopped")
            }
        } catch (e: Exception) {
            fail(revision, "停止失败: ${e.message}")
        }
    }

    private fun fail(revision: Long, message: String) {
        val observed = ServerState.status
        if (observed.revision != revision) return
        if (observed.phase == ServerState.Phase.STARTING && isReady(Prefs(this).port)) {
            if (ServerState.transition(revision, ServerState.Phase.RUNNING)) updateNotification()
            return
        }
        if (ServerState.transition(revision, ServerState.Phase.ERROR, message)) {
            ServerState.append("[app] $message")
        }
    }

    // ------------------------------------------------------------ notification

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val manager = getSystemService(NotificationManager::class.java) ?: return
        if (manager.getNotificationChannel(CHANNEL_ID) != null) return
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.notification_channel),
            NotificationManager.IMPORTANCE_LOW
        )
        channel.description = getString(R.string.notification_channel_desc)
        manager.createNotificationChannel(channel)
    }

    private fun buildNotification(): Notification {
        val port = Prefs(this).port
        val address = NetUtils.addressFor(port)

        val openIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )
        val stopIntent = PendingIntent.getService(
            this,
            1,
            Intent(this, WebScreenService::class.java).setAction(ACTION_STOP),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val builder = Notification.Builder(this, CHANNEL_ID)
            .setContentTitle(getString(R.string.app_name))
            .setContentText(getString(R.string.notification_running, address))
            .setSmallIcon(R.drawable.ic_stat_webscreen)
            .setContentIntent(openIntent)
            .setOngoing(true)
            .addAction(
                Notification.Action.Builder(
                    null,
                    getString(R.string.action_stop),
                    stopIntent
                ).build()
            )

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            builder.setForegroundServiceBehavior(Notification.FOREGROUND_SERVICE_IMMEDIATE)
        }
        return builder.build()
    }

    private fun startForegroundCompat() {
        val notification = try {
            buildNotification()
        } catch (t: Throwable) {
            Log.e(TAG, "buildNotification failed", t)
            ServerState.append("[app] 构建通知失败: ${t.javaClass.simpleName}: ${t.message}")
            return
        }

        // Android 14+ 必须声明 foregroundServiceType；而 specialUse 在侧载应用上
        // 会因缺少 Play 审核被 AppOps 直接拒绝（真机 Android 16 + targetSdk 34 实测：
        // "AppOps: Operation not started ... op=START_FOREGROUND" → 进程被杀、反复重启）。
        // 因此按"最可能被接受"的顺序依次尝试，任何一个成功即返回；
        // 全部失败也不抛异常（否则就是用户看到的"点启动就闪退"），
        // 降级为普通服务继续运行，并把真实原因写进界面日志。
        val candidates = ArrayList<Int>()
        if (Build.VERSION.SDK_INT >= 34) {
            candidates.add(ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PLAYBACK)
            candidates.add(ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE)
        }
        candidates.add(-1) // -1 = 不指定类型（老系统路径）

        var lastError: Exception? = null
        for (type in candidates) {
            try {
                if (type < 0) {
                    startForeground(NOTIFICATION_ID, notification)
                } else {
                    startForeground(NOTIFICATION_ID, notification, type)
                }
                if (type < 0) {
                    ServerState.append("[app] 前台服务已启动（未指定类型）")
                } else {
                    ServerState.append("[app] 前台服务已启动（type=$type）")
                }
                return
            } catch (e: Exception) {
                lastError = e
                ServerState.append("[app] 前台服务类型 $type 被拒绝: ${e.javaClass.simpleName}: ${e.message}")
            }
        }

        // 全部失败：不抛异常，避免进程被系统判定为崩溃后反复重启
        ServerState.append("[app] 警告：所有前台服务类型都被拒绝，降级为普通服务运行（后台可能被系统回收）")
        if (lastError != null) {
            ServerState.append("[app] 最后错误: ${lastError.javaClass.simpleName}: ${lastError.message}")
        }
    }

    private fun updateNotification() {
        try {
            val manager = getSystemService(NotificationManager::class.java)
            manager?.notify(NOTIFICATION_ID, buildNotification())
        } catch (e: Exception) {
            // ignore
        }
    }

    private fun stopForegroundCompat() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.N) {
            stopForeground(STOP_FOREGROUND_REMOVE)
        } else {
            @Suppress("DEPRECATION")
            stopForeground(true)
        }
    }

    companion object {
        const val TAG = "WebScrcpy"
        const val ACTION_START = "com.webscreen.app.START"
        const val ACTION_STOP = "com.webscreen.app.STOP"
        private const val CHANNEL_ID = "webscreen"
        private const val NOTIFICATION_ID = 4711

        fun start(context: Context) {
            val revision = ServerState.requestStart() ?: return
            ServerState.clearLog()
            val intent = Intent(context, WebScreenService::class.java).setAction(ACTION_START)
            // 优先用普通 startService：
            // Android 15+ 的部分 ROM 即使 startForeground 成功也会杀掉 FGS 进程
            // （见 onStartCommand 的说明），而 startForegroundService 还额外要求
            // 5 秒内完成 startForeground，失败即杀。普通服务两者都不触发。
            // 用户是在前台点按钮，因此 startService 不会被后台限制拒绝。
            try {
                context.startService(intent)
                return
            } catch (e: Exception) {
                Log.w(TAG, "startService 失败，退回 startForegroundService", e)
            }
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(intent)
                } else {
                    context.startService(intent)
                }
            } catch (e2: Exception) {
                Log.e(TAG, "两种方式都无法启动服务", e2)
                val message = "启动服务失败: ${e2.javaClass.simpleName}: ${e2.message}"
                ServerState.transition(revision, ServerState.Phase.ERROR, message)
                ServerState.append("[app] $message")
            }
        }

        fun stop(context: Context) {
            val revision = ServerState.requestStop() ?: return
            val intent = Intent(context, WebScreenService::class.java).setAction(ACTION_STOP)
            try {
                context.startService(intent)
            } catch (e: Exception) {
                ServerState.transition(revision, ServerState.Phase.ERROR, "停止服务失败: ${e.message}")
            }
        }

        @Volatile
        var lastProbeDetail: String = "not probed"
            private set

        private data class HttpProbe(val code: Int, val body: String)

        private fun getLocal(port: Int, path: String): HttpProbe {
            val connection = URL("http://127.0.0.1:$port$path")
                .openConnection(Proxy.NO_PROXY) as HttpURLConnection
            try {
                connection.connectTimeout = 1000
                connection.readTimeout = 1000
                connection.instanceFollowRedirects = false
                connection.useCaches = false
                val code = connection.responseCode
                // Do not read an unbounded response from a potentially unrelated listener.
                val body = if (code == 200) connection.inputStream.bufferedReader().use { reader ->
                    val chars = CharArray(4096)
                    var count = 0
                    while (count < chars.size) {
                        val n = reader.read(chars, count, chars.size - count)
                        if (n <= 0) break
                        count += n
                    }
                    String(chars, 0, count)
                } else ""
                return HttpProbe(code, body)
            } finally {
                connection.disconnect()
            }
        }

        private fun reportProbe(ready: Boolean, detail: String): Boolean {
            if (lastProbeDetail != detail) {
                lastProbeDetail = detail
                Log.i(TAG, "HTTP readiness: $detail")
            }
            return ready
        }

        /** Use an identified HTTP endpoint, not process existence or a bare TCP port. */
        fun isReady(port: Int): Boolean {
            return try {
                val health = getLocal(port, "/healthz")
                if (health.code == 200 && health.body.trim() == "WebScrcpy ready") {
                    reportProbe(true, "healthz OK")
                } else if (health.code == 404) {
                    // An old root binary may survive an APK upgrade; its public PWA
                    // manifest is available even with PIN authentication enabled.
                    val manifest = getLocal(port, "/static/manifest.json")
                    val json = if (manifest.code == 200) JSONObject(manifest.body) else null
                    val name = json?.optString("name")
                    val identified = json != null && (name == "WebScreen" || name == "WebScrcpy") &&
                        json.optString("start_url") == "/" &&
                        json.optString("display") == "standalone"
                    if (identified) reportProbe(true, "legacy WebScreen/WebScrcpy server ready; stop/start to upgrade")
                    else reportProbe(false, "healthz 404; no recognized legacy manifest")
                } else {
                    reportProbe(false, "healthz HTTP ${health.code}; unexpected readiness response")
                }
            } catch (e: Exception) {
                reportProbe(false, "${e.javaClass.simpleName}: ${e.message}")
            }
        }

        /** True when a webscreen process is alive (checked through su). */
        fun detectRunning(): Boolean {
            val result = RootShell.run(listOf("pgrep", "-f", "[w]ebscreen/webscreen"), timeoutSeconds = 20)
            if (!result.ok) return false
            val pid = result.output.trim().lines().firstOrNull()?.trim().orEmpty()
            return pid.isNotEmpty() && pid.all { it.isDigit() }
        }
    }
}
