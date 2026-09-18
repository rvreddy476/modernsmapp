package com.us.mopedu.captain

import android.content.Intent
import android.graphics.Color
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.payments.ActivityPaymentHost
import com.us.android.core.payments.PaymentResultSink
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinkBus
import com.us.android.feature.mopedu.captain.navigation.CaptainDeepLinks
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentOpener
import dagger.hilt.android.AndroidEntryPoint
import javax.inject.Inject

/**
 * The single Activity. Sign-in when there is no session; the captain console when there is.
 *
 * ## Payment result plumbing (2026-09-18)
 *
 * The captain pays their plan on the device. Razorpay delivers its result to
 * the ACTIVITY it opened onto, so this class is the [ActivityPaymentHost]
 * (from `:core:payments`) exactly as Momentum's MainActivity is: it supplies
 * [paymentResultSink] and interprets nothing. A1/R-3: a client callback is
 * evidence, never proof — the plans screen polls
 * `GET /subscriptions/me/payment` for every ending.
 *
 * The application id is "mopedu", the same as the rider's fare in Momentum:
 * ONE payments application, two references (a ride id there, a subscription id
 * here) in two processes, each with its own [com.us.android.core.payments.PaymentHandoff].
 *
 * ## Pushes
 *
 * A tapped `captain.subscription.*` / `captain.approved` / `captain.under_review`
 * push lands here with the presenter's `type` extra (the same key FCM's own
 * launch intent carries), and goes on [CaptainDeepLinkBus] for the console.
 */
@AndroidEntryPoint
class CaptainActivity : ComponentActivity(), ActivityPaymentHost {

    @Inject
    lateinit var sessionStateProvider: SessionStateProvider

    /**
     * Where the PSP's result goes — the launcher holding the in-flight sheet.
     * Only the Activity holds this seam: nothing above this line should be able
     * to inject a payment result.
     */
    @Inject
    override lateinit var paymentResultSink: PaymentResultSink

    /** The plan's opener: checkout already happened; it opens the same sheet, stamped "mopedu". */
    @Inject
    lateinit var paymentOpener: CaptainPaymentOpener

    @Inject
    lateinit var deepLinks: CaptainDeepLinkBus

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
                CaptainApp(
                    sessionStateProvider = sessionStateProvider,
                    // The Activity is what the PSP SDK opens onto and what it
                    // calls back, so the handoff starts here rather than in
                    // the plans screen.
                    onOpenPayment = { request -> paymentOpener.start(activity = this, request = request) },
                    onAbandonPayment = { request -> paymentOpener.abandon(request) },
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
        val type = intent?.getStringExtra(NotificationPresenter.KEY_TYPE) ?: return
        CaptainDeepLinks.fromPush(type)?.let(deepLinks::publish)
    }
}
