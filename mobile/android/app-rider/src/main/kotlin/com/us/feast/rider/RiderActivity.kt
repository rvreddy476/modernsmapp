package com.us.feast.rider

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.deeplink.RiderDeepLinks
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/**
 * The single Activity. Sign-in when there is no session; the rider when there is.
 *
 * Links arrive here: the DigiLocker return and `/rider/offers/{id}` as the
 * intent's data, and a tapped `food_delivery_offer` push (once FCM exists) as
 * its extras. Both go on [RiderDeepLinkBus], which the signed-in shell drains.
 */
@AndroidEntryPoint
class RiderActivity : ComponentActivity() {

    @Inject
    lateinit var sessionStateProvider: SessionStateProvider

    @Inject
    lateinit var deepLinks: RiderDeepLinkBus

    override fun onCreate(savedInstanceState: Bundle?) {
        // Navy whatever the device's night mode, so the bars draw light glyphs.
        enableEdgeToEdge(
            statusBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(Color.TRANSPARENT),
        )
        super.onCreate(savedInstanceState)
        if (savedInstanceState == null) route(intent)
        setContent {
            UsTheme {
                RiderApp(sessionStateProvider)
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
        val link = RiderDeepLinks.parse(intent.dataString)
            ?: intent.extras?.let { extras ->
                RiderDeepLinks.fromPush(PUSH_KEYS.associateWith { key -> extras.getString(key) })
            }
        link?.let(deepLinks::publish)
    }

    private companion object {
        val PUSH_KEYS = listOf("type", "offer_id", "deep_link", "order_id", "expires_at")
    }
}
