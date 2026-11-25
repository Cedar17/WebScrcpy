package com.webscreen.app

import android.os.Build
import android.util.Log

/**
 * Shared, in-process state of the webscreen server, read by the activity and
 * written by the service.
 */
object ServerState {

    const val MAX_LOG_LINES = 500

    enum class Phase { STOPPED, STARTING, RUNNING, STOPPING, ERROR }

    data class Status(val phase: Phase = Phase.STOPPED, val revision: Long = 0, val error: String? = null)

    @Volatile
    var status = Status()
        private set

    private val logLines = ArrayDeque<String>()
    private val logLock = Any()

    @Synchronized
    fun requestStart(): Long? {
        if (status.phase == Phase.STARTING || status.phase == Phase.RUNNING || status.phase == Phase.STOPPING) return null
        status = Status(Phase.STARTING, status.revision + 1)
        return status.revision
    }

    @Synchronized
    fun requestStop(): Long? {
        if (status.phase == Phase.STOPPING || status.phase == Phase.STOPPED) return null
        status = Status(Phase.STOPPING, status.revision + 1)
        return status.revision
    }

    @Synchronized
    fun transition(revision: Long, phase: Phase, error: String? = null): Boolean {
        if (status.revision != revision) return false
        status = Status(phase, revision + 1, error)
        return true
    }

    @Synchronized
    fun reconcile(observed: Status, ready: Boolean): Boolean {
        if (status != observed || observed.phase == Phase.STARTING || observed.phase == Phase.STOPPING) return false
        val next = if (ready) Phase.RUNNING else if (observed.phase == Phase.RUNNING) Phase.STOPPED else observed.phase
        if (next == observed.phase) return false
        status = Status(next, status.revision + 1)
        return true
    }

    fun append(line: String) {
        // 同时写 logcat：服务在早期被杀时界面日志来不及显示，只能靠系统日志定位。
        Log.i("WebScrcpy", line)
        synchronized(logLock) {
            if (logLines.size >= MAX_LOG_LINES) {
                logLines.removeFirst()
            }
            logLines.addLast(line)
        }
    }

    fun snapshot(): List<String> = synchronized(logLock) { logLines.toList() }

    fun clearLog() = synchronized(logLock) { logLines.clear() }

    fun deviceSummary(): String = "${Build.MANUFACTURER} ${Build.MODEL} · Android ${Build.VERSION.RELEASE}"
}
