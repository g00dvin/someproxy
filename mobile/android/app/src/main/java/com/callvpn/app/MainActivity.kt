package com.callvpn.app

import android.Manifest
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.pm.PackageManager
import android.net.VpnService
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.PowerManager
import android.provider.Settings
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.core.*
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.scale
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.ClipboardManager
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.core.content.ContextCompat
import androidx.localbroadcastmanager.content.LocalBroadcastManager
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

enum class VpnState { Disconnected, Connecting, Connected }
enum class Screen { Main, Settings, Logs, Apps, ProfileEditor }

class MainActivity : ComponentActivity() {

    private var vpnState = mutableStateOf(VpnState.Disconnected)
    private var activeConns = mutableStateOf(0)
    private var totalConns = mutableStateOf(0)
    private var logLines = mutableStateOf<List<String>>(emptyList())
    private var connectionStage = mutableStateOf("")
    private var speedTestRunning = mutableStateOf(false)
    private var speedTestPhase = mutableStateOf("")
    private var speedTestProgress = mutableStateOf("")
    private var speedTestResult = mutableStateOf("")
    private var pendingCallLink = ""
    private var pendingServerAddr = ""
    private var pendingToken = ""
    private var pendingVkTokens = ""

    private val vpnPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.StartActivityForResult()
    ) { result ->
        if (result.resultCode == RESULT_OK) {
            getSharedPreferences("callvpn", Context.MODE_PRIVATE)
                .edit().putBoolean("vpn_permission_granted", true).apply()
            startVpnService()
        } else {
            Toast.makeText(this, "VPN permission denied", Toast.LENGTH_SHORT).show()
        }
    }

    private val notificationPermissionLauncher = registerForActivityResult(
        ActivityResultContracts.RequestPermission()
    ) { /* proceed regardless */ }

    private val stateReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            val state = intent?.getStringExtra(CallVpnService.EXTRA_STATE) ?: return
            vpnState.value = when (state) {
                "connecting" -> VpnState.Connecting
                "connected" -> VpnState.Connected
                else -> VpnState.Disconnected
            }
            if (state == "disconnected") { activeConns.value = 0; totalConns.value = 0 }
        }
    }
    private val logReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            val text = intent?.getStringExtra(CallVpnService.EXTRA_LOG_TEXT) ?: return
            logLines.value = (logLines.value + text.split("\n").filter { it.isNotBlank() }).takeLast(500)
        }
    }
    private val connCountReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            activeConns.value = intent?.getIntExtra(CallVpnService.EXTRA_ACTIVE_CONNS, 0) ?: 0
            totalConns.value = intent?.getIntExtra(CallVpnService.EXTRA_TOTAL_CONNS, 0) ?: 0
        }
    }
    private val stageReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            connectionStage.value = intent?.getStringExtra(CallVpnService.EXTRA_STAGE_TEXT) ?: ""
        }
    }
    private val speedTestReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context, intent: Intent) {
            val phase = intent.getStringExtra(CallVpnService.EXTRA_SPEEDTEST_PHASE) ?: ""
            val json = intent.getStringExtra(CallVpnService.EXTRA_SPEEDTEST_JSON) ?: ""
            when (phase) {
                "complete", "error" -> {
                    speedTestRunning.value = false
                    speedTestResult.value = json
                    speedTestProgress.value = ""
                }
                else -> {
                    speedTestPhase.value = phase
                    if (json.isNotEmpty()) speedTestProgress.value = json
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
                != PackageManager.PERMISSION_GRANTED) {
                notificationPermissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
            }
        }
        requestBatteryOptimizationExemption()
        vpnState.value = when (CallVpnService.currentState) {
            "connecting" -> VpnState.Connecting
            "connected" -> VpnState.Connected
            else -> VpnState.Disconnected
        }
        val lbm = LocalBroadcastManager.getInstance(this)
        lbm.registerReceiver(stateReceiver, IntentFilter(CallVpnService.ACTION_STATE_CHANGED))
        lbm.registerReceiver(logReceiver, IntentFilter(CallVpnService.ACTION_LOG))
        lbm.registerReceiver(connCountReceiver, IntentFilter(CallVpnService.ACTION_CONN_COUNT))
        lbm.registerReceiver(stageReceiver, IntentFilter(CallVpnService.ACTION_STAGE))
        lbm.registerReceiver(speedTestReceiver, IntentFilter(CallVpnService.ACTION_SPEEDTEST_PROGRESS))
        handleQuickConnect(intent)

        setContent {
            val colors = darkColorScheme().copy(
                background = Color(0xFF121212),
                surface = Color(0xFF1E1E1E),
                surfaceVariant = Color(0xFF2A2A2A),
                primary = Color(0xFF90CAF9),
                onBackground = Color(0xFFE0E0E0),
                onSurface = Color(0xFFE0E0E0),
                onSurfaceVariant = Color(0xFF9E9E9E),
                outline = Color(0xFF424242)
            )
            MaterialTheme(colorScheme = colors) {
                Surface(modifier = Modifier.fillMaxSize(), color = MaterialTheme.colorScheme.background) {
                    AppNavigation(
                        vpnState = vpnState.value,
                        activeConns = activeConns.value,
                        totalConns = totalConns.value,
                        connectionStage = connectionStage.value,
                        logLines = logLines.value,
                        speedTestRunning = speedTestRunning.value,
                        speedTestPhase = speedTestPhase.value,
                        speedTestProgress = speedTestProgress.value,
                        speedTestResult = speedTestResult.value,
                        onSpeedTestStart = {
                            speedTestRunning.value = true
                            speedTestResult.value = ""; speedTestProgress.value = ""; speedTestPhase.value = ""
                            startService(Intent(this, CallVpnService::class.java).apply {
                                action = CallVpnService.ACTION_SPEEDTEST_START
                            })
                        },
                        onConnect = { callLink, serverAddr, token, numConns, vkTokens, serverMode ->
                            requestConnect(callLink, serverAddr, token, numConns, vkTokens, serverMode)
                        },
                        onDisconnect = { stopVpn() }
                    )
                }
            }
        }
    }

    override fun onDestroy() {
        val lbm = LocalBroadcastManager.getInstance(this)
        lbm.unregisterReceiver(stateReceiver)
        lbm.unregisterReceiver(logReceiver)
        lbm.unregisterReceiver(connCountReceiver)
        lbm.unregisterReceiver(stageReceiver)
        lbm.unregisterReceiver(speedTestReceiver)
        super.onDestroy()
    }

    private var pendingNumConns = 4
    private var pendingServerMode = "active-backup"

    private fun requestConnect(callLink: String, serverAddr: String, token: String, numConns: Int, vkTokens: String = "", serverMode: String = "active-backup") {
        pendingCallLink = callLink; pendingServerAddr = serverAddr
        pendingToken = token; pendingNumConns = numConns; pendingVkTokens = vkTokens; pendingServerMode = serverMode
        val intent = VpnService.prepare(this)
        if (intent != null) vpnPermissionLauncher.launch(intent)
        else {
            getSharedPreferences("callvpn", Context.MODE_PRIVATE)
                .edit().putBoolean("vpn_permission_granted", true).apply()
            startVpnService()
        }
    }

    private fun startVpnService() {
        vpnState.value = VpnState.Connecting
        val intent = Intent(this, CallVpnService::class.java).apply {
            action = CallVpnService.ACTION_START
            putExtra(CallVpnService.EXTRA_CALL_LINK, pendingCallLink)
            putExtra(CallVpnService.EXTRA_SERVER_ADDR, pendingServerAddr)
            putExtra(CallVpnService.EXTRA_NUM_CONNS, pendingNumConns)
            putExtra(CallVpnService.EXTRA_TOKEN, pendingToken)
            putExtra(CallVpnService.EXTRA_VK_TOKENS, pendingVkTokens)
            putExtra(CallVpnService.EXTRA_SERVER_MODE, pendingServerMode)
        }
        ContextCompat.startForegroundService(this, intent)
    }

    private fun requestBatteryOptimizationExemption() {
        val pm = getSystemService(PowerManager::class.java) ?: return
        if (!pm.isIgnoringBatteryOptimizations(packageName)) {
            try { startActivity(Intent(Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS).apply {
                data = Uri.parse("package:$packageName")
            }) } catch (_: Exception) { }
        }
    }

    private fun stopVpn() {
        startService(Intent(this, CallVpnService::class.java).apply { action = CallVpnService.ACTION_STOP })
    }

    override fun onNewIntent(intent: Intent) { super.onNewIntent(intent); handleQuickConnect(intent) }

    private fun handleQuickConnect(intent: Intent?) {
        if (intent?.action != ACTION_QUICK_CONNECT) return
        if (vpnState.value != VpnState.Disconnected) return
        val profileManager = ProfileManager(this)
        val profile = profileManager.getActiveProfile() ?: return
        val callLink = profile.callLinks.map { parseCallLink(it) }.filter { it.isNotBlank() }.joinToString(",")
        if (callLink.isBlank()) return
        val serverAddr = if (profile.connectionMode == "direct") profile.serverAddrsJoined().ifBlank { profile.effectiveServerAddr() } else ""
        val token = if (profile.connectionMode == "direct") {
            val svrs = profile.servers.filter { it.addr.isNotBlank() }
            if (svrs.isNotEmpty()) svrs.joinToString(",") { it.token } else profile.effectiveToken()
        } else ""
        requestConnect(callLink, serverAddr, token, profile.numConns, profile.vkTokensJoined(), profile.serverMode)
    }

    companion object { const val ACTION_QUICK_CONNECT = "com.callvpn.QUICK_CONNECT" }
}

private fun parseCallLink(input: String): String {
    val vkMatch = Regex("""vk\.com/call/join/([A-Za-z0-9_-]+)""").find(input)
    if (vkMatch != null) return vkMatch.groupValues[1]
    val telemostMatch = Regex("""telemost\.yandex\.\w+/j/(\d+)""").find(input)
    if (telemostMatch != null) return telemostMatch.groupValues[1]
    return input.trim()
}

// ─── Navigation ──────────────────────────────────────────────────────────────

@Composable
fun AppNavigation(
    vpnState: VpnState, activeConns: Int, totalConns: Int, connectionStage: String,
    logLines: List<String>, speedTestRunning: Boolean, speedTestPhase: String,
    speedTestProgress: String, speedTestResult: String, onSpeedTestStart: () -> Unit,
    onConnect: (String, String, String, Int, String, String) -> Unit, onDisconnect: () -> Unit
) {
    var currentScreen by remember { mutableStateOf(Screen.Main) }
    var editingProfile by remember { mutableStateOf<Profile?>(null) }
    var isNewProfile by remember { mutableStateOf(false) }

    // Handle Android system back button/gesture
    BackHandler(enabled = currentScreen != Screen.Main) {
        currentScreen = when (currentScreen) {
            Screen.Logs, Screen.Apps -> Screen.Settings
            else -> Screen.Main
        }
    }

    when (currentScreen) {
        Screen.Main -> MainScreen(
            vpnState = vpnState, activeConns = activeConns, totalConns = totalConns,
            connectionStage = connectionStage, speedTestRunning = speedTestRunning,
            speedTestPhase = speedTestPhase, speedTestProgress = speedTestProgress,
            speedTestResult = speedTestResult, onSpeedTestStart = onSpeedTestStart,
            onConnect = onConnect, onDisconnect = onDisconnect,
            onSettings = { currentScreen = Screen.Settings },
            onEditProfile = { p -> editingProfile = p; isNewProfile = false; currentScreen = Screen.ProfileEditor },
            onNewProfile = { editingProfile = null; isNewProfile = true; currentScreen = Screen.ProfileEditor }
        )
        Screen.Settings -> SettingsScreen(
            onBack = { currentScreen = Screen.Main },
            onLogs = { currentScreen = Screen.Logs },
            onApps = { currentScreen = Screen.Apps },
            vpnState = vpnState
        )
        Screen.Logs -> LogsScreen(logLines = logLines, onBack = { currentScreen = Screen.Settings })
        Screen.Apps -> AppsScreen(onBack = { currentScreen = Screen.Settings })
        Screen.ProfileEditor -> ProfileEditorScreen(
            profile = editingProfile, isNew = isNewProfile,
            onSave = { currentScreen = Screen.Main },
            onDelete = { currentScreen = Screen.Main },
            onBack = { currentScreen = Screen.Main },
            onConnect = onConnect, onDisconnect = onDisconnect, vpnState = vpnState
        )
    }
}

// ─── Main Screen ─────────────────────────────────────────────────────────────

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
fun MainScreen(
    vpnState: VpnState, activeConns: Int, totalConns: Int, connectionStage: String,
    speedTestRunning: Boolean, speedTestPhase: String, speedTestProgress: String,
    speedTestResult: String, onSpeedTestStart: () -> Unit,
    onConnect: (String, String, String, Int, String, String) -> Unit, onDisconnect: () -> Unit,
    onSettings: () -> Unit, onEditProfile: (Profile) -> Unit, onNewProfile: () -> Unit
) {
    val context = LocalContext.current
    val profileManager = remember { ProfileManager(context) }
    var profiles by remember { mutableStateOf(profileManager.getProfiles()) }
    var activeProfileId by remember { mutableStateOf(profileManager.getActiveProfileId()) }
    val activeProfile = profiles.find { it.id == activeProfileId }
    val isConnected = vpnState != VpnState.Disconnected
    val clipboardManager: ClipboardManager = LocalClipboardManager.current

    // Refresh profiles when returning to this screen
    LaunchedEffect(Unit) {
        profiles = profileManager.getProfiles()
        activeProfileId = profileManager.getActiveProfileId()
    }

    fun connectProfile(profile: Profile) {
        val callLink = profile.callLinks.map { parseCallLink(it) }.filter { it.isNotBlank() }.joinToString(",")
        val serverAddr = if (profile.connectionMode == "direct") profile.serverAddrsJoined().ifBlank { profile.effectiveServerAddr() } else ""
        val token = if (profile.connectionMode == "direct") {
            val svrs = profile.servers.filter { it.addr.isNotBlank() }
            if (svrs.isNotEmpty()) svrs.joinToString(",") { it.token } else profile.effectiveToken()
        } else ""
        onConnect(callLink, serverAddr, token, profile.numConns.coerceIn(1, 16), profile.vkTokensJoined(), profile.serverMode)
    }

    val statusText = when (vpnState) {
        VpnState.Disconnected -> "Отключён"
        VpnState.Connecting -> if (connectionStage.isNotEmpty()) connectionStage else "Подключение..."
        VpnState.Connected -> "Подключён"
    }
    val statusColor = when (vpnState) {
        VpnState.Disconnected -> Color(0xFF757575)
        VpnState.Connecting -> Color(0xFFFFB74D)
        VpnState.Connected -> Color(0xFF81C784)
    }
    val buttonColor = when (vpnState) {
        VpnState.Disconnected -> if (activeProfile != null) Color(0xFF424242) else Color(0xFF303030)
        VpnState.Connecting -> Color(0xFF5D4037)
        VpnState.Connected -> Color(0xFF424242)
    }
    val buttonBorderColor = when (vpnState) {
        VpnState.Disconnected -> if (activeProfile != null) Color(0xFF616161) else Color(0xFF424242)
        VpnState.Connecting -> Color(0xFFFFB74D)
        VpnState.Connected -> Color(0xFF81C784)
    }
    val buttonText = when (vpnState) {
        VpnState.Disconnected -> "Подключить"
        VpnState.Connecting -> "Отмена"
        VpnState.Connected -> "Отключить"
    }

    Column(modifier = Modifier.fillMaxSize()) {
        // Top bar with settings
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            horizontalArrangement = Arrangement.End
        ) {
            IconButton(onClick = onSettings) {
                Icon(Icons.Default.Settings, "Настройки", tint = Color(0xFF9E9E9E))
            }
        }

        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally
        ) {
            Spacer(modifier = Modifier.height(40.dp))

            // Status
            Text(statusText, fontSize = 16.sp, color = statusColor, fontWeight = FontWeight.Medium)

            // Connection count
            if (vpnState != VpnState.Disconnected && totalConns > 0) {
                Spacer(modifier = Modifier.height(4.dp))
                Text(
                    "$activeConns / $totalConns", fontSize = 13.sp,
                    color = if (activeConns == totalConns) Color(0xFF757575) else Color(0xFFFFB74D),
                    fontFamily = FontFamily.Monospace
                )
            }

            Spacer(modifier = Modifier.height(40.dp))

            // Connect button with pulse animation during connecting
            val pulseAnim = rememberInfiniteTransition(label = "pulse")
            val pulseScale by pulseAnim.animateFloat(
                initialValue = 1f, targetValue = 1.06f,
                animationSpec = infiniteRepeatable(
                    animation = tween(800, easing = EaseInOut),
                    repeatMode = RepeatMode.Reverse
                ), label = "scale"
            )
            val btnModifier = if (vpnState == VpnState.Connecting)
                Modifier.size(160.dp).scale(pulseScale) else Modifier.size(160.dp)

            OutlinedButton(
                onClick = {
                    when (vpnState) {
                        VpnState.Disconnected -> activeProfile?.let { connectProfile(it) }
                        else -> onDisconnect()
                    }
                },
                modifier = btnModifier,
                shape = CircleShape,
                enabled = vpnState != VpnState.Disconnected || activeProfile != null,
                colors = ButtonDefaults.outlinedButtonColors(containerColor = buttonColor),
                border = BorderStroke(2.dp, buttonBorderColor)
            ) {
                Text(buttonText, fontSize = 15.sp, fontWeight = FontWeight.Medium, color = Color(0xFFE0E0E0))
            }

            Spacer(modifier = Modifier.height(40.dp))

            // Profile badges
            FlowRow(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp)
            ) {
                for (profile in profiles) {
                    val isActive = profile.id == activeProfileId
                    Surface(
                        modifier = Modifier.height(36.dp).clickable {
                            if (!isActive) {
                                if (isConnected) onDisconnect()
                                activeProfileId = profile.id
                                profileManager.setActiveProfileId(profile.id)
                                connectProfile(profile)
                            } else {
                                onEditProfile(profile)
                            }
                        },
                        shape = RoundedCornerShape(18.dp),
                        color = if (isActive) Color(0xFF2A2A2A) else Color(0xFF1E1E1E),
                        border = BorderStroke(1.dp, if (isActive) Color(0xFF616161) else Color(0xFF383838))
                    ) {
                        Row(
                            modifier = Modifier.padding(horizontal = 14.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(6.dp)
                        ) {
                            // Connection status dot for active profile
                            if (isActive && vpnState != VpnState.Disconnected) {
                                val dotColor = if (vpnState == VpnState.Connected) Color(0xFF81C784) else Color(0xFFFFB74D)
                                Surface(modifier = Modifier.size(8.dp), shape = CircleShape, color = dotColor) {}
                            }
                            Text(
                                if (profile.isTelemostLink()) "Y" else "VK", fontSize = 11.sp,
                                fontWeight = FontWeight.Bold,
                                color = if (isActive) Color(0xFFE0E0E0) else Color(0xFF757575)
                            )
                            Text(
                                profile.name.ifBlank { "Без имени" }, fontSize = 13.sp,
                                color = if (isActive) Color(0xFFE0E0E0) else Color(0xFF9E9E9E)
                            )
                        }
                    }
                }

                // Add
                Surface(
                    modifier = Modifier.height(36.dp).clickable { onNewProfile() },
                    shape = RoundedCornerShape(18.dp),
                    color = Color(0xFF1E1E1E),
                    border = BorderStroke(1.dp, Color(0xFF383838))
                ) {
                    Row(
                        modifier = Modifier.padding(horizontal = 14.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(4.dp)
                    ) {
                        Icon(Icons.Default.Add, null, modifier = Modifier.size(16.dp), tint = Color(0xFF757575))
                        Text("Добавить", fontSize = 12.sp, color = Color(0xFF757575))
                    }
                }

                // Import
                Surface(
                    modifier = Modifier.height(36.dp).clickable {
                        val clipText = clipboardManager.getText()?.text
                        if (clipText.isNullOrBlank()) {
                            Toast.makeText(context, "Буфер обмена пуст", Toast.LENGTH_SHORT).show()
                            return@clickable
                        }
                        try {
                            val imported = Profile.fromExportJson(clipText)
                            profileManager.saveProfile(imported)
                            profiles = profileManager.getProfiles()
                            if (activeProfileId == null) {
                                activeProfileId = imported.id
                                profileManager.setActiveProfileId(imported.id)
                            }
                            Toast.makeText(context, "Импортировано", Toast.LENGTH_SHORT).show()
                        } catch (_: Exception) {
                            Toast.makeText(context, "Неверный формат", Toast.LENGTH_SHORT).show()
                        }
                    },
                    shape = RoundedCornerShape(18.dp),
                    color = Color(0xFF1E1E1E),
                    border = BorderStroke(1.dp, Color(0xFF383838))
                ) {
                    Row(
                        modifier = Modifier.padding(horizontal = 14.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Text("Импорт", fontSize = 12.sp, color = Color(0xFF757575))
                    }
                }
            }

            // Speed test
            if (vpnState == VpnState.Connected) {
                Spacer(modifier = Modifier.height(24.dp))
                OutlinedButton(
                    onClick = { onSpeedTestStart() }, enabled = !speedTestRunning,
                    modifier = Modifier.fillMaxWidth(), shape = RoundedCornerShape(12.dp),
                    border = BorderStroke(1.dp, Color(0xFF424242))
                ) {
                    Text(if (speedTestRunning) "Тестирование..." else "Тест скорости",
                        color = Color(0xFF9E9E9E), fontSize = 13.sp)
                }
                if (speedTestRunning && speedTestProgress.isNotEmpty()) {
                    Spacer(modifier = Modifier.height(8.dp))
                    val txt = runCatching {
                        val j = org.json.JSONObject(speedTestProgress)
                        "${speedTestPhase}: ${"%.2f".format(j.optDouble("current_mbps", 0.0))} Mbps"
                    }.getOrDefault("${speedTestPhase}...")
                    Text(txt, fontSize = 12.sp, color = Color(0xFF90CAF9))
                }
                if (speedTestResult.isNotEmpty() && !speedTestRunning) {
                    Spacer(modifier = Modifier.height(8.dp))
                    SpeedTestResults(speedTestResult)
                }
            }

            Spacer(modifier = Modifier.height(16.dp))
        }
    }
}

@Composable
fun SpeedTestResults(json: String) {
    Card(
        modifier = Modifier.fillMaxWidth(),
        colors = CardDefaults.cardColors(containerColor = Color(0xFF1E1E1E)),
        shape = RoundedCornerShape(12.dp)
    ) {
        Column(modifier = Modifier.padding(12.dp)) {
            val lines = runCatching {
                val j = org.json.JSONObject(json)
                buildList {
                    j.optJSONObject("ping")?.let { add("Ping: ${"%.1f".format(it.optDouble("avg_ms"))}ms (jitter ${"%.1f".format(it.optDouble("jitter_ms"))}ms)") }
                    j.optJSONObject("download")?.let { add("Download: ${"%.2f".format(it.optDouble("mbps"))} Mbps") }
                    j.optJSONObject("upload")?.let { add("Upload: ${"%.2f".format(it.optDouble("mbps"))} Mbps") }
                }
            }.getOrNull()
            lines?.forEach { Text(it, fontSize = 12.sp, color = Color(0xFFE0E0E0)) }
                ?: Text(json, fontSize = 11.sp, color = Color(0xFF9E9E9E))
        }
    }
}

// ─── Settings Screen ─────────────────────────────────────────────────────────

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SettingsScreen(onBack: () -> Unit, onLogs: () -> Unit, onApps: () -> Unit, vpnState: VpnState) {
    val context = LocalContext.current
    val rootManager = remember { RootManager(context) }
    var rootAvailable by remember { mutableStateOf(false) }
    var hotspotRouting by remember { mutableStateOf(rootManager.hotspotRoutingEnabled) }

    LaunchedEffect(Unit) {
        rootAvailable = rootManager.isRootAvailable()
        if (!rootAvailable && hotspotRouting) {
            hotspotRouting = false; rootManager.hotspotRoutingEnabled = false
        }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        // Top bar
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = Color(0xFF9E9E9E)) }
            Text("Настройки", fontSize = 18.sp, fontWeight = FontWeight.Medium, color = Color(0xFFE0E0E0))
        }

        val appsManager = remember { ExcludedAppsManager(context) }
        var whitelistEnabled by remember { mutableStateOf(appsManager.isWhitelistEnabled()) }

        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(2.dp)
        ) {
            val isVpnActive = vpnState != VpnState.Disconnected

            // Hint when VPN is active
            if (isVpnActive) {
                Text(
                    "Отключите VPN для изменения настроек",
                    fontSize = 11.sp, color = Color(0xFFFFB74D),
                    modifier = Modifier.padding(horizontal = 8.dp, vertical = 8.dp)
                )
            }

            // Whitelist toggle
            Row(
                modifier = Modifier.fillMaxWidth().padding(vertical = 12.dp, horizontal = 8.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text("Белый список приложений", fontSize = 14.sp,
                        color = if (isVpnActive) Color(0xFF757575) else Color(0xFFE0E0E0))
                    Text("VPN только для выбранных приложений", fontSize = 11.sp, color = Color(0xFF757575))
                }
                Switch(
                    checked = whitelistEnabled,
                    onCheckedChange = { whitelistEnabled = it; appsManager.setWhitelistEnabled(it) },
                    enabled = !isVpnActive,
                    colors = SwitchDefaults.colors(
                        checkedThumbColor = Color.White, checkedTrackColor = Color(0xFF616161),
                        uncheckedThumbColor = Color(0xFF9E9E9E), uncheckedTrackColor = Color(0xFF2A2A2A)
                    )
                )
            }
            HorizontalDivider(color = Color(0xFF2A2A2A))

            // App selection (only when whitelist enabled)
            if (whitelistEnabled) {
                SettingsItem(title = "Выбор приложений", subtitle = "Приложения для работы через VPN", onClick = onApps)
            }

            // Logs
            SettingsItem(title = "Логи", subtitle = "Просмотр и экспорт логов подключения", onClick = onLogs)

            // Hotspot
            if (rootAvailable) {
                Spacer(modifier = Modifier.height(8.dp))
                Row(
                    modifier = Modifier.fillMaxWidth().padding(vertical = 12.dp, horizontal = 8.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween
                ) {
                    Column(modifier = Modifier.weight(1f)) {
                        Text("WiFi Hotspot через VPN", fontSize = 14.sp,
                            color = if (isVpnActive) Color(0xFF757575) else Color(0xFFE0E0E0))
                        Text("Маршрутизация раздачи + TTL 64", fontSize = 11.sp, color = Color(0xFF757575))
                    }
                    Switch(
                        checked = hotspotRouting,
                        onCheckedChange = { hotspotRouting = it; rootManager.hotspotRoutingEnabled = it },
                        enabled = !isVpnActive,
                        colors = SwitchDefaults.colors(
                            checkedThumbColor = Color.White, checkedTrackColor = Color(0xFF616161),
                            uncheckedThumbColor = Color(0xFF9E9E9E), uncheckedTrackColor = Color(0xFF2A2A2A)
                        )
                    )
                }
            }

            // Version
            Spacer(modifier = Modifier.height(24.dp))
            Text(
                "v${BuildConfig.VERSION_NAME}",
                fontSize = 11.sp, color = Color(0xFF616161),
                modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp),
            )
            Spacer(modifier = Modifier.height(16.dp))
        }
    }
}

@Composable
fun SettingsItem(title: String, subtitle: String, onClick: () -> Unit) {
    Surface(
        modifier = Modifier.fillMaxWidth().clickable { onClick() },
        color = Color.Transparent
    ) {
        Row(
            modifier = Modifier.padding(vertical = 14.dp, horizontal = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            Column(modifier = Modifier.weight(1f)) {
                Text(title, fontSize = 14.sp, color = Color(0xFFE0E0E0))
                Text(subtitle, fontSize = 11.sp, color = Color(0xFF757575))
            }
            Icon(Icons.Default.KeyboardArrowRight, null, tint = Color(0xFF616161))
        }
    }
    HorizontalDivider(color = Color(0xFF2A2A2A))
}

@Composable
fun SectionHeader(icon: androidx.compose.ui.graphics.vector.ImageVector, title: String,
                  count: String? = null, onAdd: (() -> Unit)? = null) {
    Spacer(modifier = Modifier.height(8.dp))
    HorizontalDivider(color = Color(0xFF2A2A2A))
    Row(
        modifier = Modifier.fillMaxWidth().padding(vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically
    ) {
        Icon(icon, null, modifier = Modifier.size(16.dp), tint = Color(0xFF616161))
        Spacer(modifier = Modifier.width(8.dp))
        Text(title, fontSize = 13.sp, fontWeight = FontWeight.Medium, color = Color(0xFF9E9E9E))
        if (count != null) {
            Text("  $count", fontSize = 12.sp, color = Color(0xFF616161))
        }
        Spacer(modifier = Modifier.weight(1f))
        if (onAdd != null) {
            IconButton(onClick = onAdd, modifier = Modifier.size(28.dp)) {
                Icon(Icons.Default.Add, null, modifier = Modifier.size(16.dp), tint = Color(0xFF757575))
            }
        }
    }
}

// ─── Logs Screen ─────────────────────────────────────────────────────────────

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LogsScreen(logLines: List<String>, onBack: () -> Unit) {
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    val listState = rememberLazyListState()
    LaunchedEffect(logLines.size) {
        if (logLines.isNotEmpty()) listState.animateScrollToItem(logLines.size - 1)
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = Color(0xFF9E9E9E)) }
            Text("Логи", fontSize = 18.sp, fontWeight = FontWeight.Medium, color = Color(0xFFE0E0E0),
                modifier = Modifier.weight(1f))
            // Share / export
            IconButton(onClick = {
                if (logLines.isEmpty()) return@IconButton
                val text = logLines.joinToString("\n")
                val sendIntent = Intent(Intent.ACTION_SEND).apply {
                    type = "text/plain"
                    putExtra(Intent.EXTRA_TEXT, text)
                    putExtra(Intent.EXTRA_SUBJECT, "CallVPN Logs")
                }
                context.startActivity(Intent.createChooser(sendIntent, "Экспорт логов"))
            }) { Icon(Icons.Default.Share, "Экспорт", tint = Color(0xFF9E9E9E)) }
            // Copy
            IconButton(onClick = {
                if (logLines.isNotEmpty()) {
                    clipboard.setText(AnnotatedString(logLines.joinToString("\n")))
                    Toast.makeText(context, "Скопировано", Toast.LENGTH_SHORT).show()
                }
            }) { Icon(Icons.Default.ContentCopy, "Копировать", tint = Color(0xFF9E9E9E)) }
        }

        if (logLines.isEmpty()) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("Нет записей", fontSize = 13.sp, color = Color(0xFF616161))
            }
        } else {
            LazyColumn(
                state = listState,
                modifier = Modifier.fillMaxSize().padding(horizontal = 12.dp, vertical = 8.dp)
            ) {
                items(logLines.size) { index ->
                    val line = logLines[index]
                    val c = when {
                        line.contains("level=ERROR") || line.startsWith("ERROR:") -> Color(0xFFEF9A9A)
                        line.contains("level=WARN") -> Color(0xFFFFCC80)
                        else -> Color(0xFF9E9E9E)
                    }
                    Text(line, fontSize = 10.sp, fontFamily = FontFamily.Monospace, color = c)
                }
            }
        }
    }
}

// ─── Apps Screen ─────────────────────────────────────────────────────────────

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun AppsScreen(onBack: () -> Unit) {
    val context = LocalContext.current
    val manager = remember { ExcludedAppsManager(context) }
    var selectedPackages by remember { mutableStateOf(manager.getWhitelistPackages()) }
    var allApps by remember { mutableStateOf<List<AppInfo>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var searchQuery by remember { mutableStateOf("") }

    LaunchedEffect(Unit) {
        val apps = withContext(Dispatchers.IO) {
            val pm = context.packageManager
            try {
                pm.getInstalledApplications(0).mapNotNull { appInfo ->
                    try {
                        if (pm.getLaunchIntentForPackage(appInfo.packageName) == null) return@mapNotNull null
                        if (appInfo.packageName in ExcludedAppsManager.FORCED_PACKAGES) return@mapNotNull null
                        AppInfo(appInfo.packageName, try { appInfo.loadLabel(pm).toString() } catch (_: Exception) { appInfo.packageName })
                    } catch (_: Exception) { null }
                }.sortedBy { it.label.lowercase() }
            } catch (_: Exception) { emptyList() }
        }
        allApps = apps; loading = false
    }

    val filtered = remember(allApps, searchQuery) {
        if (searchQuery.isBlank()) allApps
        else allApps.filter { it.label.lowercase().contains(searchQuery.lowercase()) || it.packageName.contains(searchQuery.lowercase()) }
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = Color(0xFF9E9E9E)) }
            Text("Выбор приложений", fontSize = 18.sp, fontWeight = FontWeight.Medium, color = Color(0xFFE0E0E0),
                modifier = Modifier.weight(1f))
            // Select all / Deselect all
            TextButton(onClick = {
                if (selectedPackages.size == allApps.size) {
                    selectedPackages = emptySet()
                } else {
                    selectedPackages = allApps.map { it.packageName }.toSet()
                }
                manager.setWhitelistPackages(selectedPackages)
            }) {
                Text(
                    if (selectedPackages.size == allApps.size && allApps.isNotEmpty()) "Снять все" else "Выбрать все",
                    fontSize = 12.sp, color = Color(0xFF90CAF9)
                )
            }
        }

        Text(
            "Выбрано: ${selectedPackages.size}",
            fontSize = 12.sp, color = Color(0xFF9E9E9E), fontWeight = FontWeight.Medium,
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 2.dp)
        )
        Text(
            "Отмеченные приложения будут работать через VPN",
            fontSize = 11.sp, color = Color(0xFF757575),
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 2.dp)
        )

        // Search
        OutlinedTextField(
            value = searchQuery, onValueChange = { searchQuery = it },
            placeholder = { Text("Поиск", fontSize = 13.sp) },
            leadingIcon = { Icon(Icons.Default.Search, null, modifier = Modifier.size(18.dp)) },
            singleLine = true, shape = RoundedCornerShape(12.dp),
            modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 4.dp)
        )

        if (loading) {
            Box(modifier = Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                Text("Загрузка...", fontSize = 13.sp, color = Color(0xFF757575))
            }
        } else {
            LazyColumn(modifier = Modifier.fillMaxSize()) {
                items(filtered, key = { it.packageName }) { app ->
                    val isSelected = app.packageName in selectedPackages
                    Row(
                        modifier = Modifier.fillMaxWidth()
                            .clickable {
                                selectedPackages = if (isSelected) selectedPackages - app.packageName
                                    else selectedPackages + app.packageName
                                manager.setWhitelistPackages(selectedPackages)
                            }
                            .padding(horizontal = 16.dp, vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically
                    ) {
                        Column(modifier = Modifier.weight(1f)) {
                            Text(app.label, fontSize = 13.sp, color = Color(0xFFE0E0E0))
                            Text(app.packageName, fontSize = 10.sp, color = Color(0xFF616161))
                        }
                        Checkbox(
                            checked = isSelected,
                            onCheckedChange = { checked ->
                                selectedPackages = if (checked) selectedPackages + app.packageName
                                    else selectedPackages - app.packageName
                                manager.setWhitelistPackages(selectedPackages)
                            },
                            colors = CheckboxDefaults.colors(checkedColor = Color(0xFF757575), checkmarkColor = Color.White)
                        )
                    }
                }
            }
        }
    }
}

data class AppInfo(val packageName: String, val label: String)

// ─── Profile Editor Screen ───────────────────────────────────────────────────

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ProfileEditorScreen(
    profile: Profile?, isNew: Boolean,
    onSave: () -> Unit, onDelete: () -> Unit, onBack: () -> Unit,
    onConnect: (String, String, String, Int, String, String) -> Unit, onDisconnect: () -> Unit,
    vpnState: VpnState
) {
    val context = LocalContext.current
    val profileManager = remember { ProfileManager(context) }
    val base = profile ?: Profile()
    val clipboard = LocalClipboardManager.current

    var name by remember { mutableStateOf(base.name) }
    var connectionMode by remember { mutableStateOf(base.connectionMode) }
    var callLinks by remember { mutableStateOf(base.callLinks.ifEmpty { listOf("") }) }
    var numConns by remember { mutableStateOf(base.numConns.toString()) }
    var vkTokens by remember { mutableStateOf(base.vkTokens) }
    var servers by remember { mutableStateOf(base.servers.ifEmpty { listOf(ServerEntry()) }) }
    var serverMode by remember { mutableStateOf(base.serverMode) }
    var showDeleteConfirm by remember { mutableStateOf(false) }

    fun buildProfile(): Profile {
        val conns = numConns.toIntOrNull()?.coerceIn(1, 16) ?: 4
        val svrs = servers.filter { it.addr.isNotBlank() }
        return base.copy(
            name = name.trim(), connectionMode = connectionMode,
            callLinks = callLinks.map { it.trim() },
            serverAddr = svrs.firstOrNull()?.addr ?: "",
            token = svrs.firstOrNull()?.token ?: "",
            numConns = conns,
            vkTokens = vkTokens.map { it.trim() }.filter { it.isNotEmpty() },
            servers = svrs, serverMode = serverMode
        )
    }

    Column(modifier = Modifier.fillMaxSize()) {
        Row(
            modifier = Modifier.fillMaxWidth().padding(horizontal = 8.dp, vertical = 8.dp),
            verticalAlignment = Alignment.CenterVertically
        ) {
            IconButton(onClick = onBack) { Icon(Icons.Default.ArrowBack, "Назад", tint = Color(0xFF9E9E9E)) }
            Text(
                if (isNew) "Новый профиль" else "Редактирование",
                fontSize = 18.sp, fontWeight = FontWeight.Medium, color = Color(0xFFE0E0E0),
                modifier = Modifier.weight(1f)
            )
            TextButton(onClick = {
                val saved = buildProfile()
                profileManager.saveProfile(saved)
                val activeId = profileManager.getActiveProfileId()
                if (activeId == null) profileManager.setActiveProfileId(saved.id)
                if (saved.id == activeId && vpnState != VpnState.Disconnected) {
                    onDisconnect()
                }
                onSave()
            }) { Text("Сохранить", color = Color(0xFF90CAF9)) }
        }

        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 16.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp)
        ) {
            // ── Basic ──
            OutlinedTextField(
                value = name, onValueChange = { if (it.length <= 20) name = it },
                label = { Text("Имя профиля") }, placeholder = { Text("Мой VPN") },
                singleLine = true, modifier = Modifier.fillMaxWidth()
            )
            Spacer(modifier = Modifier.height(2.dp))
            Text("Тип подключения", fontSize = 12.sp, color = Color(0xFF757575))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilterChip(selected = connectionMode == "relay", onClick = { connectionMode = "relay" },
                    label = { Text("Relay", fontSize = 12.sp) },
                    colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Color(0xFF2A2A2A)))
                FilterChip(selected = connectionMode == "direct", onClick = { connectionMode = "direct" },
                    label = { Text("Direct", fontSize = 12.sp) },
                    colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Color(0xFF2A2A2A)))
            }

            // ── Call links ──
            SectionHeader(icon = Icons.Default.Link, title = "Ссылки", count = "${callLinks.size}/8",
                onAdd = if (callLinks.size < 8) {{ callLinks = callLinks + "" }} else null)
            callLinks.forEachIndexed { i, link ->
                Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = link, onValueChange = { v -> callLinks = callLinks.toMutableList().also { it[i] = v } },
                        label = { Text("Ссылка ${i + 1}") }, placeholder = { Text("https://vk.com/call/join/...") },
                        singleLine = true, modifier = Modifier.weight(1f)
                    )
                    if (callLinks.size > 1) {
                        IconButton(onClick = { callLinks = callLinks.toMutableList().also { it.removeAt(i) } },
                            modifier = Modifier.size(28.dp)) {
                            Icon(Icons.Default.Close, null, modifier = Modifier.size(16.dp), tint = Color(0xFFEF9A9A))
                        }
                    }
                }
            }

            // ── Servers (direct mode) ──
            if (connectionMode == "direct") {
                SectionHeader(icon = Icons.Default.Dns, title = "Серверы", count = "${servers.size}",
                    onAdd = { servers = servers + ServerEntry() })

                if (servers.size > 1) {
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(selected = serverMode == "active-backup",
                            onClick = { serverMode = "active-backup" },
                            label = { Text("Active-Backup", fontSize = 11.sp) },
                            colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Color(0xFF2A2A2A)))
                        FilterChip(selected = serverMode == "load-balance",
                            onClick = { serverMode = "load-balance" },
                            label = { Text("Балансировка", fontSize = 11.sp) },
                            colors = FilterChipDefaults.filterChipColors(selectedContainerColor = Color(0xFF2A2A2A)))
                    }
                    Text(
                        if (serverMode == "active-backup") "Резервный сервер при недоступности основного"
                        else "Соединения распределяются между серверами",
                        fontSize = 10.sp, color = Color(0xFF616161)
                    )
                }

                servers.forEachIndexed { i, server ->
                    Card(
                        modifier = Modifier.fillMaxWidth(),
                        colors = CardDefaults.cardColors(containerColor = Color(0xFF1E1E1E)),
                        shape = RoundedCornerShape(8.dp)
                    ) {
                        Column(modifier = Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween,
                                verticalAlignment = Alignment.CenterVertically) {
                                Text("Сервер ${i + 1}", fontSize = 12.sp, color = Color(0xFF9E9E9E))
                                if (servers.size > 1) {
                                    IconButton(onClick = { servers = servers.toMutableList().also { it.removeAt(i) } },
                                        modifier = Modifier.size(24.dp)) {
                                        Icon(Icons.Default.Close, null, Modifier.size(14.dp), tint = Color(0xFFEF9A9A))
                                    }
                                }
                            }
                            OutlinedTextField(
                                value = server.addr,
                                onValueChange = { v -> servers = servers.toMutableList().also { it[i] = it[i].copy(addr = v) } },
                                label = { Text("Адрес (host:port)") }, singleLine = true, modifier = Modifier.fillMaxWidth()
                            )
                            OutlinedTextField(
                                value = server.token,
                                onValueChange = { v -> servers = servers.toMutableList().also { it[i] = it[i].copy(token = v) } },
                                label = { Text("Токен") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                                visualTransformation = PasswordVisualTransformation()
                            )
                        }
                    }
                }
            }

            // ── Connection settings ──
            SectionHeader(icon = Icons.Default.Tune, title = "Параметры")
            OutlinedTextField(
                value = numConns,
                onValueChange = { if (it.isEmpty() || it.all { c -> c.isDigit() }) numConns = it },
                label = { Text("Подключения (1-16)") }, singleLine = true, modifier = Modifier.fillMaxWidth(),
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number)
            )

            // ── VK tokens ──
            SectionHeader(icon = Icons.Default.Key, title = "VK токены", count = "${vkTokens.size}/16",
                onAdd = if (vkTokens.size < 16) {{ vkTokens = vkTokens + "" }} else null)
            if (vkTokens.isEmpty()) {
                Text("Ускоряют подключение", fontSize = 10.sp, color = Color(0xFF616161),
                    modifier = Modifier.padding(start = 4.dp))
            }
            vkTokens.forEachIndexed { i, t ->
                Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = t, onValueChange = { v -> vkTokens = vkTokens.toMutableList().also { it[i] = v } },
                        label = { Text("Токен ${i + 1}") }, singleLine = true, modifier = Modifier.weight(1f),
                        visualTransformation = PasswordVisualTransformation()
                    )
                    IconButton(onClick = { vkTokens = vkTokens.toMutableList().also { it.removeAt(i) } },
                        modifier = Modifier.size(28.dp)) {
                        Icon(Icons.Default.Close, null, Modifier.size(16.dp), tint = Color(0xFFEF9A9A))
                    }
                }
            }

            // ── Actions ──
            Spacer(modifier = Modifier.height(4.dp))
            HorizontalDivider(color = Color(0xFF2A2A2A))
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                TextButton(onClick = {
                    clipboard.setText(AnnotatedString(buildProfile().toExportJson()))
                    Toast.makeText(context, "Скопировано", Toast.LENGTH_SHORT).show()
                }) { Text("Экспорт", color = Color(0xFF757575), fontSize = 13.sp) }

                if (!isNew) {
                    TextButton(onClick = { showDeleteConfirm = true }) {
                        Text("Удалить", color = Color(0xFFEF9A9A), fontSize = 13.sp)
                    }
                }
            }

            Spacer(modifier = Modifier.height(32.dp))
        }
    }

    if (showDeleteConfirm && profile != null) {
        AlertDialog(
            onDismissRequest = { showDeleteConfirm = false },
            title = { Text("Удалить профиль?") },
            text = { Text("\"${profile.name.ifBlank { "Без имени" }}\" будет удалён.") },
            confirmButton = {
                TextButton(onClick = {
                    showDeleteConfirm = false
                    val wasActive = profile.id == profileManager.getActiveProfileId()
                    if (wasActive && vpnState != VpnState.Disconnected) onDisconnect()
                    profileManager.deleteProfile(profile.id)
                    onDelete()
                }) { Text("Удалить", color = Color(0xFFEF9A9A)) }
            },
            dismissButton = { TextButton(onClick = { showDeleteConfirm = false }) { Text("Отмена") } }
        )
    }
}
