package com.us.android.feature.commerce

import android.app.Activity
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession

/**
 * The REAL [PaymentCoordinator], over a launcher that cannot open a sheet.
 *
 * The checkout ViewModel only ever calls [PaymentCoordinator.confirm] — the
 * sheet is opened from the Activity, and its outcome reaches the ViewModel on
 * the handoff bus these tests publish to directly. A call to `open` here would
 * be a wiring mistake, so it fails loudly.
 */
internal fun pollingOnlyPaymentCoordinator(): PaymentCoordinator =
    PaymentCoordinator(
        object : PaymentLauncher {
            override fun open(
                activity: Activity,
                attempt: PaymentAttempt,
                session: PaymentSession,
                onOutcome: (PaymentOutcome) -> Unit,
            ) = error("the checkout ViewModel must never open a sheet itself")

            override fun abandon(attempt: PaymentAttempt) = Unit
        },
    )
