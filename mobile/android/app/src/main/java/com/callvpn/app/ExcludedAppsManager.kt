package com.callvpn.app

import android.content.Context
import android.content.SharedPreferences
import org.json.JSONArray

/**
 * Manages per-app VPN routing:
 * - off (default): VPN for all apps, only FORCED_PACKAGES are excluded
 * - whitelist: VPN ONLY for selected apps
 */
class ExcludedAppsManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("callvpn", Context.MODE_PRIVATE)

    /** Returns true if whitelist mode is enabled. */
    fun isWhitelistEnabled(): Boolean {
        return prefs.getBoolean(KEY_WHITELIST_ENABLED, false)
    }

    fun setWhitelistEnabled(enabled: Boolean) {
        prefs.edit().putBoolean(KEY_WHITELIST_ENABLED, enabled).apply()
    }

    /** Returns the set of packages selected for VPN in whitelist mode. */
    fun getWhitelistPackages(): Set<String> {
        val json = prefs.getString(KEY_WHITELIST, null) ?: return emptySet()
        return try {
            val arr = JSONArray(json)
            val set = mutableSetOf<String>()
            for (i in 0 until arr.length()) {
                set.add(arr.getString(i))
            }
            // Never include forced packages — they must always bypass VPN
            set - FORCED_PACKAGES
        } catch (_: Exception) {
            emptySet()
        }
    }

    fun setWhitelistPackages(packages: Set<String>) {
        val arr = JSONArray()
        (packages - FORCED_PACKAGES).forEach { arr.put(it) }
        prefs.edit().putString(KEY_WHITELIST, arr.toString()).apply()
    }

    companion object {
        private const val KEY_WHITELIST_ENABLED = "whitelist_enabled"
        private const val KEY_WHITELIST = "whitelist_packages_json"

        /** Apps that MUST always be excluded from VPN — user cannot route them through VPN. */
        val FORCED_PACKAGES = setOf(
            "com.callvpn.app",
            "com.vkontakte.android",
            "com.vk.im",
            "com.vk.admin",
            "com.vk.video",
            "ru.rutube.app",
            "ru.rostel.gosuslugi",
            "ru.mail.max",
        )
    }
}
