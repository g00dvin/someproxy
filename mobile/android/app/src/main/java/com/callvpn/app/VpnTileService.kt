package com.callvpn.app

import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.os.Build
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import androidx.localbroadcastmanager.content.LocalBroadcastManager

class VpnTileService : TileService() {

    private val stateReceiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            updateTile()
        }
    }

    override fun onStartListening() {
        super.onStartListening()
        LocalBroadcastManager.getInstance(this)
            .registerReceiver(stateReceiver, IntentFilter(CallVpnService.ACTION_STATE_CHANGED))
        updateTile()
    }

    override fun onStopListening() {
        LocalBroadcastManager.getInstance(this)
            .unregisterReceiver(stateReceiver)
        super.onStopListening()
    }

    override fun onClick() {
        super.onClick()

        when (CallVpnService.currentState) {
            "connected", "connecting" -> {
                val intent = Intent(this, CallVpnService::class.java).apply {
                    action = CallVpnService.ACTION_STOP
                }
                startService(intent)
            }
            else -> {
                // Launch Activity which will start the foreground VPN service.
                // Starting FGS directly from TileService is blocked on Android 14/16.
                val activityIntent = Intent(this, MainActivity::class.java).apply {
                    action = MainActivity.ACTION_QUICK_CONNECT
                    addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                }
                collapseAndStartActivity(activityIntent)
            }
        }
    }

    @Suppress("DEPRECATION")
    private fun collapseAndStartActivity(intent: Intent) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
            val pending = PendingIntent.getActivity(
                this, 0, intent,
                PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_IMMUTABLE
            )
            startActivityAndCollapse(pending)
        } else {
            startActivityAndCollapse(intent)
        }
    }

    private fun updateTile() {
        val tile = qsTile ?: return
        when (CallVpnService.currentState) {
            "connected" -> {
                tile.state = Tile.STATE_ACTIVE
                tile.subtitle = "Подключён"
            }
            "connecting" -> {
                tile.state = Tile.STATE_ACTIVE
                tile.subtitle = "Подключение..."
            }
            else -> {
                tile.state = Tile.STATE_INACTIVE
                tile.subtitle = null
            }
        }
        tile.label = "CallVPN"
        tile.updateTile()
    }
}
