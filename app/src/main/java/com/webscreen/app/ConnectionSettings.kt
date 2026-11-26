package com.webscreen.app

import android.content.Context
import java.net.Inet4Address
import java.net.NetworkInterface

class Prefs(context: Context) {

    private val sp = context.getSharedPreferences("webscreen", Context.MODE_PRIVATE)

    var port: Int
        get() = sp.getInt("port", DEFAULT_PORT)
        set(value) = sp.edit().putInt("port", value).apply()

    var pin: String
        get() = sp.getString("pin", "") ?: ""
        set(value) = sp.edit().putString("pin", value).apply()

    var autoStart: Boolean
        get() = sp.getBoolean("auto_start", false)
        set(value) = sp.edit().putBoolean("auto_start", value).apply()

    companion object {
        const val DEFAULT_PORT = 8079
    }
}

object NetUtils {

    /** Best guess of the LAN address other devices can reach this phone at. */
    fun localIpAddress(): String? {
        val candidates = ArrayList<Pair<String, String>>() // (name, address)
        try {
            val interfaces = NetworkInterface.getNetworkInterfaces() ?: return null
            for (nif in interfaces) {
                if (!nif.isUp || nif.isLoopback) continue
                for (address in nif.inetAddresses) {
                    if (address is Inet4Address && !address.isLoopbackAddress && address.isSiteLocalAddress) {
                        candidates.add(Pair(nif.name, address.hostAddress ?: continue))
                    }
                }
            }
        } catch (e: Exception) {
            return null
        }
        // prefer the usual Wi-Fi interface
        for (preferred in listOf("wlan0", "wlan1", "ap0", "eth0")) {
            for (candidate in candidates) {
                if (candidate.first == preferred) return candidate.second
            }
        }
        return candidates.firstOrNull()?.second
    }

    fun addressFor(port: Int): String {
        val ip = localIpAddress() ?: return "http://<手机IP>:$port"
        return "http://$ip:$port"
    }
}
