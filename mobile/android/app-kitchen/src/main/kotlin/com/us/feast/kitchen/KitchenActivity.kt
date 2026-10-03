package com.us.feast.kitchen

import android.graphics.Color
import android.os.Bundle
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

    override fun onCreate(savedInstanceState: Bundle?) {
        // The theme follows the device's light / dark setting (2026-10-02), and so do the bars' glyphs.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        setContent {
            UsTheme {
                KitchenApp(sessionStateProvider)
            }
        }
    }
}
