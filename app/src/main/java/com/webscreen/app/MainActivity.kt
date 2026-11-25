package com.webscreen.app

import android.app.Activity
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.Switch
import android.widget.TextView
import android.widget.Toast

class MainActivity : Activity() {

    private lateinit var prefs: Prefs
    private val handler = Handler(Looper.getMainLooper())
    private var lastLogSize = 0

    private lateinit var rootStatus: TextView
    private lateinit var serverStatus: TextView
    private lateinit var addressView: TextView
    private lateinit var portView: TextView
    private lateinit var logView: TextView
    private lateinit var logScroll: ScrollView
    private lateinit var startButton: Button
    private lateinit var stopButton: Button
    private lateinit var pinInput: EditText
    private lateinit var autoStartSwitch: Switch

    private var rootGranted = false
    private var rootCheckInFlight = false
    private var pendingStart = false
    private var stateProbeInFlight = false
    private var failedProbes = 0

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        prefs = Prefs(this)

        rootStatus = findViewById(R.id.rootStatus)
        serverStatus = findViewById(R.id.serverStatus)
        addressView = findViewById(R.id.addressView)
        portView = findViewById(R.id.portView)
        logView = findViewById(R.id.logView)
        logScroll = findViewById(R.id.logScroll)
        startButton = findViewById(R.id.startButton)
        stopButton = findViewById(R.id.stopButton)
        pinInput = findViewById(R.id.pinInput)
        autoStartSwitch = findViewById(R.id.autoStartSwitch)

        prefs.pin.takeIf { it.isNotEmpty() }?.let { pinInput.setText(it) }
        autoStartSwitch.isChecked = prefs.autoStart
        autoStartSwitch.setOnCheckedChangeListener { _, checked ->
            prefs.autoStart = checked
        }

        findViewById<Button>(R.id.copyButton).setOnClickListener { copyAddress() }
        startButton.setOnClickListener { onStartClicked() }
        stopButton.setOnClickListener { onStopClicked() }
        findViewById<Button>(R.id.clearLogButton).setOnClickListener {
            ServerState.clearLog()
            renderLog()
        }

        requestNotificationPermissionIfNeeded()
    }

    override fun onResume() {
        super.onResume()
        checkRoot()
        refreshFromProcess()
        handler.post(tick)
    }

    override fun onPause() {
        handler.removeCallbacks(tick)
        super.onPause()
    }

    // ------------------------------------------------------------------ clicks

    private fun onStartClicked() {
        if (!rootGranted) {
            pendingStart = true
            checkRoot()
            return
        }
        startServerNow()
    }

    private fun onStopClicked() {
        WebScreenService.stop(this)
        renderServer()
    }

    private fun copyAddress() {
        val address = NetUtils.addressFor(prefs.port)
        val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
        clipboard.setPrimaryClip(ClipData.newPlainText("WebScrcpy", address))
        toast(getString(R.string.copied, address))
    }

    // ------------------------------------------------------------------ status

    private fun checkRoot() {
        if (rootCheckInFlight) return
        rootCheckInFlight = true
        Thread {
            val granted = RootShell.hasRoot()
            handler.post {
                rootGranted = granted
                rootCheckInFlight = false
                renderRoot(granted)
                if (granted && pendingStart) {
                    pendingStart = false
                    startServerNow()
                }
            }
        }.start()
    }

    private fun startServerNow() {
        val pin = pinInput.text.toString().trim()
        if (pin.isNotEmpty() && (pin.length != 6 || !pin.all { it in '0'..'9' })) {
            toast(getString(R.string.invalid_pin))
            return
        }
        prefs.pin = pin
        failedProbes = 0
        WebScreenService.start(this)
        renderServer()
    }

    private fun refreshFromProcess() {
        renderServer()
        val observed = ServerState.status
        if (stateProbeInFlight || observed.phase == ServerState.Phase.STOPPING) return
        stateProbeInFlight = true
        val port = prefs.port
        Thread {
            val ready = WebScreenService.isReady(port)
            handler.post {
                stateProbeInFlight = false
                if (ServerState.status == observed) {
                    if (ready) {
                        failedProbes = 0
                        ServerState.reconcile(observed, true)
                    } else if (observed.phase == ServerState.Phase.RUNNING) {
                        if (++failedProbes >= 3) {
                            ServerState.reconcile(observed, false)
                            failedProbes = 0
                        }
                    } else {
                        failedProbes = 0
                        // A lost service callback must not leave STARTING stuck forever.
                        if (ServerState.reconcile(observed, false)) {
                            ServerState.append("[app] Startup watchdog: " + WebScreenService.lastProbeDetail)
                        }
                    }
                }
                renderServer()
            }
        }.start()
    }

    private var lastStateProbeAt = 0L

    private val tick = object : Runnable {
        override fun run() {
            renderServer()
            renderLog()
            if (!rootGranted) checkRoot()
            val now = android.os.SystemClock.elapsedRealtime()
            if (now - lastStateProbeAt >= 2000) {
                lastStateProbeAt = now
                refreshFromProcess()
            }
            handler.postDelayed(this, 500)
        }
    }

    private fun renderRoot(granted: Boolean) {
        if (granted) {
            rootStatus.text = getString(R.string.root_granted)
            rootStatus.setTextColor(0xFF6EC7AD.toInt())
        } else {
            rootStatus.text = getString(R.string.need_root)
            rootStatus.setTextColor(0xFFD98B72.toInt())
        }
        renderServer()
    }

    private fun renderServer() {
        val state = ServerState.status
        val phase = state.phase
        serverStatus.text = when (phase) {
            ServerState.Phase.STARTING -> getString(R.string.server_starting)
            ServerState.Phase.RUNNING -> getString(R.string.server_running)
            ServerState.Phase.STOPPING -> getString(R.string.server_stopping)
            ServerState.Phase.ERROR -> getString(R.string.server_error, state.error.orEmpty())
            ServerState.Phase.STOPPED -> getString(R.string.server_stopped)
        }
        serverStatus.setTextColor(when (phase) {
            ServerState.Phase.RUNNING -> 0xFF6EC7AD.toInt()
            ServerState.Phase.STARTING, ServerState.Phase.STOPPING -> 0xFFE7B66E.toInt()
            ServerState.Phase.ERROR -> 0xFFD98B72.toInt()
            ServerState.Phase.STOPPED -> 0xFF9E9E9E.toInt()
        })

        portView.text = prefs.port.toString()
        val address = NetUtils.addressFor(prefs.port)
        addressView.text = address
        startButton.isEnabled = rootGranted && (phase == ServerState.Phase.STOPPED || phase == ServerState.Phase.ERROR)
        stopButton.isEnabled = phase == ServerState.Phase.RUNNING || phase == ServerState.Phase.ERROR ||
            phase == ServerState.Phase.STARTING
    }

    private fun renderLog() {
        val lines = ServerState.snapshot()
        if (lines.size == lastLogSize && logView.text.isNotEmpty()) return
        lastLogSize = lines.size
        logView.text = if (lines.isEmpty()) getString(R.string.log_empty) else lines.joinToString("\n")
        logScroll.post { logScroll.fullScroll(View.FOCUS_DOWN) }
    }

    // ------------------------------------------------------------------ misc

    private fun requestNotificationPermissionIfNeeded() {
        if (Build.VERSION.SDK_INT >= 33) {
            if (checkSelfPermission(android.Manifest.permission.POST_NOTIFICATIONS) !=
                android.content.pm.PackageManager.PERMISSION_GRANTED
            ) {
                requestPermissions(arrayOf(android.Manifest.permission.POST_NOTIFICATIONS), 1)
            }
        }
    }

    private fun toast(message: String) {
        Toast.makeText(this, message, Toast.LENGTH_SHORT).show()
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        refreshFromProcess()
    }
}
