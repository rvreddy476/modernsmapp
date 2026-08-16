package com.us.android

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.navigation.MainViewModel
import com.us.android.navigation.UsApp
import dagger.hilt.android.AndroidEntryPoint

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

    private val viewModel: MainViewModel by viewModels()

    override fun onCreate(savedInstanceState: Bundle?) {
        installSplashScreen()
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)

        setContent {
            UsTheme {
                UsApp(viewModel)
            }
        }
    }
}
