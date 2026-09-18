package com.us.mopedu.captain

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

/** The single Activity. Sign-in when there is no session; the captain console when there is. */
@AndroidEntryPoint
class CaptainActivity : ComponentActivity() {

    @Inject
    lateinit var sessionStateProvider: SessionStateProvider

    override fun onCreate(savedInstanceState: Bundle?) {
        // Navy whatever the device's night mode, so the bars draw light glyphs.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        setContent {
            UsTheme {
                CaptainApp(sessionStateProvider)
            }
        }
    }
}
