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
import com.razorpay.PaymentData
import com.razorpay.PaymentResultWithDataListener
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.navigation.MainViewModel
import com.us.android.navigation.UsApp
import com.us.android.payment.CheckoutPaymentCoordinator
import com.us.android.payment.PaymentSheetOutcome
import com.us.android.payment.RazorpayPaymentLauncher
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
class MainActivity : ComponentActivity(), PaymentResultWithDataListener {

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
     * Razorpay delivers its result to the ACTIVITY, not to whoever opened the
     * sheet, so the Activity has to implement the listener and forward it.
     * Injected as the concrete type because `deliver` is the forwarding seam
     * and is not part of the [com.us.android.payment.PaymentLauncher] port —
     * nothing above this line should be able to inject a payment result.
     */
    @Inject
    lateinit var razorpayLauncher: RazorpayPaymentLauncher

    @Inject
    lateinit var paymentCoordinator: CheckoutPaymentCoordinator

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
                        paymentCoordinator.start(
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
                        razorpayLauncher.abandon(attempt)
                    },
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

    // ─── Razorpay result plumbing ────────────────────────────────────
    //
    // The SDK calls back HERE rather than on the code that opened the sheet,
    // so these two overrides exist purely to forward it. They deliberately
    // interpret nothing: A1/R-3 says a client callback is evidence, never
    // proof, and the checkout flow polls the server for both outcomes. An
    // Activity that decided "paid" from onPaymentSuccess would be asserting
    // something no one has verified.

    override fun onPaymentSuccess(razorpayPaymentId: String?, paymentData: PaymentData?) {
        razorpayLauncher.deliver(PaymentSheetOutcome.Succeeded(razorpayPaymentId))
    }

    override fun onPaymentError(code: Int, response: String?, paymentData: PaymentData?) {
        // A user-cancelled sheet and a genuine provider error arrive through
        // the same callback. Both are reported as-is; the coordinator treats
        // every ending the same way, because a reported failure can still sit
        // on top of a capture that completed.
        razorpayLauncher.deliver(PaymentSheetOutcome.Failed(code, response))
    }
}
