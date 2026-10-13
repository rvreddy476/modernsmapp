package com.us.feast.kitchen

import android.graphics.Color
import android.os.Bundle
import android.content.Intent
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import com.us.android.feature.kitchen.navigation.KitchenPushTarget
import com.us.android.feature.kitchen.navigation.kitchenPushTarget
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.theme.UsTheme
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/** The single Activity. Sign-in when there is no session; the kitchen when there is. */
@AndroidEntryPoint
class KitchenActivity : ComponentActivity() {

    @Inject
    lateinit var sessionStateProvider: SessionStateProvider
    private var pushTarget by mutableStateOf<KitchenPushTarget?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        // The theme follows the device's light / dark setting (2026-10-02), and so do the bars' glyphs.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        readPush(intent)
        setContent {
            UsTheme {
                KitchenApp(sessionStateProvider, pushTarget) { pushTarget = null }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        readPush(intent)
    }

    private fun readPush(intent: Intent?) {
        pushTarget = kitchenPushTarget(intent?.getStringExtra("type"), intent?.getStringExtra("deep_link"))
    }
}
