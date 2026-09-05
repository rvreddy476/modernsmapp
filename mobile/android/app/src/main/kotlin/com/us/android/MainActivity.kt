package com.us.android

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.navigation.MainViewModel
import com.us.android.navigation.UsApp
import com.us.android.push.PushDestinations
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/**
 * The single Activity. Every screen is a Compose destination inside [UsNavHost].
 *
 * Note there is no `setKeepOnScreenCondition` on the splash: holding the
 * splash while awaiting session restore is exactly the cold-start stall
 * (finding F5) this architecture exists to avoid. Phase 2 wires the nav
 * graph to observe SessionState instead, so the first frame is never blocked.
 */
@AndroidEntryPoint
class MainActivity : ComponentActivity() {

    /**
     * Injected here rather than into the reels screen so the pool outlives any
     * one composable. It holds decoder sessions and audio focus; scoping it to
     * a screen the pager recomposes would release and reacquire them mid-scroll.
     */
    @Inject
    lateinit var playerPool: PlayerPool

    @Inject
    lateinit var pushDestinations: PushDestinations

    private val viewModel: MainViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        // The app is navy whatever the device's night mode, so the system
        // bars must draw light glyphs over it rather than follow the system.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        offerPushDestination(intent)
        pushDestinations.offerLink(intent?.data)

        setContent {
            UsTheme {
                UsApp(viewModel, playerPool)
            }
        }
    }

    /**
     * A notification tap while this (singleTop) activity is alive lands here
     * rather than in a fresh onCreate — without this override, tapping a
     * chat notification with the app backgrounded brought it forward on
     * whatever screen it was showing and went nowhere.
     */
    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        offerPushDestination(intent)
        // An App Link (atpost.app/chat/join/…) arriving on the live activity.
        pushDestinations.offerLink(intent.data)
    }

    /**
     * Reads the push routing extras. Background (system-rendered) taps carry
     * the FCM data payload as launch-intent extras; foreground taps carry the
     * same keys via the presenter's content intent — one contract, one path.
     */
    private fun offerPushDestination(intent: Intent?) {
        pushDestinations.offer(
            type = intent?.getStringExtra(NotificationPresenter.KEY_TYPE),
            entityId = intent?.getStringExtra(NotificationPresenter.KEY_ENTITY_ID),
            deepLink = intent?.getStringExtra(NotificationPresenter.KEY_DEEP_LINK),
        )
    }
}
