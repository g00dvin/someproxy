package com.callvpn.app

import android.content.Context
import android.content.SharedPreferences
import org.json.JSONArray

/**
 * Manages per-app VPN routing with two modes:
 * - blacklist (default): VPN for all apps EXCEPT selected
 * - whitelist: VPN ONLY for selected apps
 */
class ExcludedAppsManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("callvpn", Context.MODE_PRIVATE)

    /** Returns "blacklist" or "whitelist". */
    fun getRoutingMode(): String {
        return prefs.getString(KEY_ROUTING_MODE, "blacklist") ?: "blacklist"
    }

    fun setRoutingMode(mode: String) {
        prefs.edit().putString(KEY_ROUTING_MODE, mode).apply()
    }

    /** In blacklist mode: apps excluded from VPN. In whitelist mode: apps included in VPN. */
    fun getSelectedPackages(): Set<String> {
        val mode = getRoutingMode()
        val json = prefs.getString(if (mode == "whitelist") KEY_WHITELIST else KEY_EXCLUDED, null)
            ?: return if (mode == "whitelist") emptySet() else DEFAULT_PACKAGES.toSet()
        return try {
            val arr = JSONArray(json)
            val set = mutableSetOf<String>()
            for (i in 0 until arr.length()) {
                set.add(arr.getString(i))
            }
            if (mode == "blacklist") {
                // Always include forced packages in blacklist mode
                set + FORCED_PACKAGES
            } else {
                // In whitelist mode, never include forced packages (they must bypass VPN)
                set - FORCED_PACKAGES
            }
        } catch (_: Exception) {
            if (mode == "whitelist") emptySet() else DEFAULT_PACKAGES.toSet()
        }
    }

    /** Legacy compat */
    fun getExcludedPackages(): Set<String> = getSelectedPackages()

    fun setSelectedPackages(packages: Set<String>) {
        val mode = getRoutingMode()
        val arr = JSONArray()
        val effective = if (mode == "blacklist") packages + FORCED_PACKAGES else packages - FORCED_PACKAGES
        effective.forEach { arr.put(it) }
        val key = if (mode == "whitelist") KEY_WHITELIST else KEY_EXCLUDED
        prefs.edit().putString(key, arr.toString()).apply()
    }

    /** Legacy compat */
    fun setExcludedPackages(packages: Set<String>) = setSelectedPackages(packages)

    fun isInitialized(): Boolean = prefs.contains(KEY_EXCLUDED)

    companion object {
        private const val KEY_EXCLUDED = "excluded_packages_json"
        private const val KEY_WHITELIST = "whitelist_packages_json"
        private const val KEY_ROUTING_MODE = "app_routing_mode"

        /** Apps that MUST always be excluded — user cannot include them in VPN. */
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

        /** Default excluded packages (used on first launch). */
        val DEFAULT_PACKAGES = listOf(
            "com.callvpn.app",
            "com.vkontakte.android",
            "com.vk.im",
            "com.vk.admin",
            "ru.vk.store",
            "com.vk.video",
            "ru.mail.max",
            "com.yandex.browser",
            "com.yandex.browser.lite",
            "ru.yandex.searchplugin",
            "ru.yandex.yandexmaps",
            "ru.yandex.taxi",
            "com.yandex.music",
            "com.yandex.disk",
            "ru.yandex.mail",
            "com.yandex.marketplace",
            "ru.yandex.weatherplugin",
            "ru.yandex.translate",
            "com.yandex.lavka",
            "ru.kinopoisk",
            "ru.kinopoisk.plus",
            "com.yandex.mobile.drive",
            "ru.mts.mymts",
            "ru.megafon.mlk",
            "ru.beeline.services",
            "ru.tele2.mytele2",
            "ru.rt.lk",
            "ru.yota.android",
            "ru.rutube.app",
            "ru.more.play",
            "ru.ivi.client",
            "ru.russianpost.postapp",
            "ru.minstroyrf.giszhkh",
            "ru.rostel.gosuslugi",
            "ru.sberbankmobile",
            "ru.sberbank.sberbankid",
            "ru.sberbank.spasibo",
            "com.idamob.tinkoff.android",
            "ru.tinkoff.investing",
            "ru.tinkoff.insurance",
            "ru.vtb24.mobilebanking.android",
            "ru.alfabank.mobile.android",
            "ru.raiffeisennews",
            "com.openbank",
            "ru.sovcomcard.halva",
            "ru.rosbank.android",
            "ru.psbank.online",
            "ru.bpc.mobilebank.android",
            "ru.rshb.mbank",
            "ru.mw",
            "com.unicredit.mob",
            "ru.letobank.Prometheus",
            "ru.yoomoney.android",
            "ru.homecredit.mycredit",
        )
    }
}
