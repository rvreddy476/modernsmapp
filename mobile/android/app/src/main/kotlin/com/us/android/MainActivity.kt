package com.us.android

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.activity.viewModels
import androidx.core.splashscreen.SplashScreen.Companion.installSplashScreen
import androidx.lifecycle.lifecycleScope
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.payments.ActivityPaymentHost
import com.us.android.core.payments.PaymentResultSink
import com.us.android.feature.commerce.checkout.CheckoutPaymentOpener
import com.us.android.feature.dating.premium.DatingPaymentOpener
import com.us.android.feature.feast.checkout.FeastPaymentOpener
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentOpener
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
 *
 * ## Payment result plumbing
 *
 * The PSP SDK calls back on THIS Activity rather than on the code that opened
 * the sheet. [ActivityPaymentHost] (from `:core:payments`) is that listener
 * with the forwarding written once: it hands the SDK's result to
 * [paymentResultSink] and interprets nothing. A1/R-3 says a client callback is
 * evidence, never proof, and checkout polls the server for every ending.
 */
@AndroidEntryPoint
class MainActivity : ComponentActivity(), ActivityPaymentHost {

    /**
     * Injected here rather than into the reels screen so the pool outlives any
     * one composable. It holds decoder sessions and audio focus; scoping it to
     * a screen the pager recomposes would release and reacquire them mid-scroll.
     */
    @Inject
    lateinit var playerPool: PlayerPool

    @Inject
    lateinit var pushDestinations: PushDestinations

    /**
     * Where the PSP's result goes — the launcher holding the in-flight sheet.
     * Only the Activity holds this seam: nothing above this line should be able
     * to inject a payment result.
     */
    @Inject
    override lateinit var paymentResultSink: PaymentResultSink

    @Inject
    lateinit var paymentOpener: CheckoutPaymentOpener

    /** Feast's opener: its own intent route, its own application id, the same sheet and bus. */
    @Inject
    lateinit var feastPaymentOpener: FeastPaymentOpener

    /** Dating Premium's opener: the purchase already exists; it opens the same sheet, stamped "dating". */
    @Inject
    lateinit var datingPaymentOpener: DatingPaymentOpener

    /** Mopedu's opener: the ride's intent already exists; it opens the same sheet, stamped "mopedu". */
    @Inject
    lateinit var mopeduPaymentOpener: MopeduPaymentOpener

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
                UsApp(
                    viewModel = viewModel,
                    pool = playerPool,
                    // The Activity is what the PSP SDK opens onto and what it
                    // calls back, so the handoff starts here rather than
                    // somewhere in the Compose tree that would have to hunt
                    // for an Activity in a LocalContext.
                    onOpenPaymentSheet = { attempt, orderNumber ->
                        paymentOpener.start(
                            activity = this,
                            scope = lifecycleScope,
                            attempt = attempt,
                            orderNumber = orderNumber,
                        )
                    },
                    // C3-LB-4: releases the launcher's single in-flight slot
                    // when the checkout screen goes away, so a buyer who backs
                    // out mid-sheet is not refused on every later attempt.
                    onAbandonPaymentSheet = { attempt ->
                        paymentOpener.abandon(attempt)
                    },
                    onOpenFeastPayment = { request ->
                        feastPaymentOpener.start(activity = this, scope = lifecycleScope, request = request)
                    },
                    onAbandonFeastPayment = { request -> feastPaymentOpener.abandon(request) },
                    onOpenDatingPayment = { request -> datingPaymentOpener.start(activity = this, request = request) },
                    onAbandonDatingPayment = { request -> datingPaymentOpener.abandon(request) },
                    onOpenMopeduPayment = { request -> mopeduPaymentOpener.start(activity = this, request = request) },
                    onAbandonMopeduPayment = { request -> mopeduPaymentOpener.abandon(request) },
                )
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
