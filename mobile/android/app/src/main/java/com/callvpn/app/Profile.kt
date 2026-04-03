package com.callvpn.app

import android.content.Context
import android.content.SharedPreferences
import org.json.JSONArray
import org.json.JSONObject
import java.util.UUID

data class Profile(
    val id: String = UUID.randomUUID().toString(),
    val name: String = "",
    val connectionMode: String = "relay", // "relay" or "direct"
    val callLinks: List<String> = listOf(""),
    val serverAddr: String = "",
    val token: String = "",
    val numConns: Int = 4,
    val vkTokens: List<String> = emptyList()
) {
    // Compat: first non-empty link for provider detection
    val callLink: String get() = callLinks.firstOrNull { it.isNotBlank() } ?: ""

    fun isTelemostLink(): Boolean {
        val link = callLink
        return link.contains("telemost.yandex") ||
                (link.all { it.isDigit() } && link.length > 10)
    }

    // Comma-separated for Go tunnel
    fun callLinksJoined(): String = callLinks.filter { it.isNotBlank() }.joinToString(",")
    fun vkTokensJoined(): String = vkTokens.filter { it.isNotBlank() }.joinToString(",")

    fun toJson(): JSONObject = JSONObject().apply {
        put("id", id)
        put("name", name)
        put("connectionMode", connectionMode)
        put("callLinks", JSONArray(callLinks))
        put("serverAddr", serverAddr)
        put("token", token)
        put("numConns", numConns)
        if (vkTokens.isNotEmpty()) put("vkTokens", JSONArray(vkTokens))
    }

    /** Export-friendly JSON without internal id (a new id is assigned on import). */
    fun toExportJson(): String = JSONObject().apply {
        put("name", name)
        put("connectionMode", connectionMode)
        put("callLinks", JSONArray(callLinks.filter { it.isNotBlank() }))
        if (serverAddr.isNotBlank()) put("serverAddr", serverAddr)
        if (token.isNotBlank()) put("token", token)
        put("numConns", numConns)
        if (vkTokens.isNotEmpty()) put("vkTokens", JSONArray(vkTokens.filter { it.isNotBlank() }))
    }.toString(2)

    companion object {
        /** Import a profile from exported JSON text. Always assigns a new id. */
        fun fromExportJson(text: String): Profile {
            val obj = JSONObject(text)
            return fromJson(obj).copy(id = UUID.randomUUID().toString())
        }

        fun fromJson(obj: JSONObject): Profile {
            // Migration: old "callLink" string → new "callLinks" list
            val callLinks = when {
                obj.has("callLinks") -> {
                    val arr = obj.getJSONArray("callLinks")
                    (0 until arr.length()).map { arr.getString(it) }
                }
                obj.has("callLink") -> {
                    val link = obj.optString("callLink", "")
                    if (link.isNotEmpty()) listOf(link) else listOf("")
                }
                else -> listOf("")
            }

            val vkTokens = when {
                obj.has("vkTokens") -> {
                    val v = obj.get("vkTokens")
                    if (v is JSONArray) {
                        (0 until v.length()).map { v.getString(it) }
                    } else {
                        // Migration: old comma-separated string
                        val s = v.toString()
                        if (s.isNotEmpty()) s.split(",").map { it.trim() } else emptyList()
                    }
                }
                else -> emptyList()
            }

            return Profile(
                id = obj.optString("id", UUID.randomUUID().toString()),
                name = obj.optString("name", ""),
                connectionMode = obj.optString("connectionMode", "relay"),
                callLinks = callLinks,
                serverAddr = obj.optString("serverAddr", ""),
                token = obj.optString("token", ""),
                numConns = obj.optInt("numConns", 4),
                vkTokens = vkTokens
            )
        }
    }
}

class ProfileManager(context: Context) {
    private val prefs: SharedPreferences =
        context.getSharedPreferences("callvpn", Context.MODE_PRIVATE)

    init {
        migrateFromLegacy()
    }

    fun getProfiles(): List<Profile> {
        val json = prefs.getString("profiles_json", null) ?: return emptyList()
        return try {
            val arr = JSONArray(json)
            (0 until arr.length()).map { Profile.fromJson(arr.getJSONObject(it)) }
        } catch (_: Exception) {
            emptyList()
        }
    }

    fun getActiveProfileId(): String? {
        return prefs.getString("active_profile_id", null)
    }

    fun getActiveProfile(): Profile? {
        val id = getActiveProfileId() ?: return null
        return getProfiles().find { it.id == id }
    }

    fun setActiveProfileId(id: String?) {
        prefs.edit().putString("active_profile_id", id).apply()
    }

    fun saveProfile(profile: Profile) {
        val profiles = getProfiles().toMutableList()
        val idx = profiles.indexOfFirst { it.id == profile.id }
        if (idx >= 0) {
            profiles[idx] = profile
        } else {
            profiles.add(profile)
        }
        saveProfiles(profiles)
    }

    fun deleteProfile(id: String) {
        val profiles = getProfiles().toMutableList()
        val idx = profiles.indexOfFirst { it.id == id }
        if (idx < 0) return
        profiles.removeAt(idx)
        saveProfiles(profiles)

        // If deleted profile was active, select next available
        if (getActiveProfileId() == id) {
            val nextActive = if (profiles.isNotEmpty()) {
                profiles[idx.coerceAtMost(profiles.size - 1)].id
            } else null
            setActiveProfileId(nextActive)
        }
    }

    private fun saveProfiles(profiles: List<Profile>) {
        val arr = JSONArray()
        profiles.forEach { arr.put(it.toJson()) }
        prefs.edit().putString("profiles_json", arr.toString()).apply()
    }

    private fun migrateFromLegacy() {
        // Already migrated if profiles exist
        if (prefs.contains("profiles_json")) return

        val callLink = prefs.getString("call_link", "") ?: ""
        // Only migrate if there's actual data
        if (callLink.isBlank()) return

        val serverAddr = prefs.getString("server_addr", "") ?: ""
        val token = prefs.getString("token", "") ?: ""
        val numConns = prefs.getInt("num_conns", 4)
        val mode = prefs.getString("connection_mode", "Relay") ?: "Relay"

        val profile = Profile(
            name = "Default",
            connectionMode = if (mode == "Direct") "direct" else "relay",
            callLinks = if (callLink.isNotEmpty()) listOf(callLink) else listOf(""),
            serverAddr = serverAddr,
            token = token,
            numConns = numConns
        )

        saveProfiles(listOf(profile))
        setActiveProfileId(profile.id)

        // Clean up legacy keys
        prefs.edit()
            .remove("call_link")
            .remove("server_addr")
            .remove("token")
            .remove("num_conns")
            .remove("connection_mode")
            .remove("recent_ids")
            .apply()
    }
}
