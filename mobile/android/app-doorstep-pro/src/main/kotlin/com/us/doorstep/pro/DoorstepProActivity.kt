package com.us.doorstep.pro

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinks
import com.us.android.feature.doorsteppro.deeplink.ProLinkConfig
import com.us.android.feature.doorsteppro.deeplink.ProPushTypes
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/**
 * The single Activity. Sign-in when there is no session; the professional
 * when there is.
 *
 * Links arrive here: the DigiLocker return and `doorstep-pro://…` as the
 * intent's data, and a tapped `doorstep.pro.*` push as its extras (the same
 * keys whether the system or NotificationPresenter rendered it). Both go on
 * [ProDeepLinkBus], which the signed-in shell drains.
 */
@AndroidEntryPoint
class DoorstepProActivity : ComponentActivity() {

    @Inject
    lateinit var sessionStateProvider: SessionStateProvider

    @Inject
    lateinit var deepLinks: ProDeepLinkBus

    @Inject
    lateinit var linkConfig: ProLinkConfig

    override fun onCreate(savedInstanceState: Bundle?) {
        // The theme follows the device's light / dark setting, and so do the bars' glyphs.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.auto(Color.TRANSPARENT, Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) route(intent)
        setContent {
            UsTheme {
                DoorstepProApp(sessionStateProvider)
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        route(intent)
    }

    private fun route(intent: Intent?) {
        intent ?: return
        val link = ProDeepLinks.parse(intent.dataString, linkConfig.allowCustomSchemeDigiLocker)
            ?: intent.extras?.let { extras ->
                ProDeepLinks.fromPush(ProPushTypes.INTENT_KEYS.associateWith { key -> extras.getString(key) })
            }
        link?.let(deepLinks::publish)
    }
}
