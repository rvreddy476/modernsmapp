package com.us.doorstep.pro

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.setValue
import androidx.compose.runtime.mutableStateOf
import com.us.android.core.payments.ActivityPaymentHost
import com.us.android.core.payments.PaymentResultSink
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentOpener
import com.us.android.feature.rider.deeplink.RiderDeepLinkBus
import com.us.android.feature.rider.deeplink.RiderDeepLinks
import com.us.doorstep.pro.partner.PartnerBusiness
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinkBus
import com.us.android.feature.doorsteppro.deeplink.ProDeepLinks
import com.us.android.feature.doorsteppro.deeplink.ProLinkConfig
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
class DoorstepProActivity : ComponentActivity(), ActivityPaymentHost {
    @Inject override lateinit var paymentResultSink: PaymentResultSink
    @Inject lateinit var paymentOpener: CaptainPaymentOpener
    @Inject lateinit var riderLinks: RiderDeepLinkBus
    @Inject lateinit var notificationRouter: com.us.doorstep.pro.partner.PartnerNotificationRouter
    private var requestedBusiness by mutableStateOf<PartnerBusiness?>(null)

    @Inject
    lateinit var foreground: com.us.android.core.notifications.AppForegroundState

    override fun onStart() {
        super.onStart()
        foreground.isForeground = true
    }

    override fun onStop() {
        foreground.isForeground = false
        super.onStop()
    }

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
            UsTheme(darkTheme = false) {
                DoorstepProApp(
                    sessionStateProvider, requestedBusiness,
                    onRequestConsumed = { requestedBusiness = null },
                    onOpenPayment = { paymentOpener.start(this, it) },
                    onAbandonPayment = paymentOpener::abandon,
                )
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
        intent.getStringExtra("partner_work_category")?.let { category ->
            val business = when (category) {
                "HOME" -> PartnerBusiness.HOME
                "MOPED" -> PartnerBusiness.MOPED
                "FOOD" -> PartnerBusiness.FOOD_DELIVERY
                "COMMERCE" -> PartnerBusiness.COMMERCE_DELIVERY
                else -> null
            }
            business?.let { notificationRouter.openRequests(it); requestedBusiness = it }
        }
        val link = ProDeepLinks.parse(intent.dataString, linkConfig.allowCustomSchemeDigiLocker)
        link?.let { deepLinks.publish(it); requestedBusiness = PartnerBusiness.HOME }
        val data = listOf("type", "offer_id", "deep_link").associateWith { intent.getStringExtra(it) }
        RiderDeepLinks.parse(intent.dataString)?.let {
            riderLinks.publish(it); requestedBusiness = PartnerBusiness.FOOD_DELIVERY
        }
        notificationRouter.open(data["type"].orEmpty(), intent.getStringExtra("entity_id").orEmpty(),
            data["deep_link"].orEmpty(), data["offer_id"])?.let { requestedBusiness = it }
    }
}
