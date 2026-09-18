// MatchingDeclarationName: this file is the feature's navigation contract.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.mopedu.captain.navigation

import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.mopedu.captain.MopeduCaptainRoute
import com.us.android.feature.mopedu.captain.payment.CaptainPaymentRequest
import kotlinx.serialization.Serializable

/** The captain console: one destination; the ViewModel owns the phases within it. */
@Serializable
data object MopeduCaptainRoute

/**
 * Registers the captain console. [onSignOut] is `:app-captain`'s edge;
 * [onOpenPayment] and [onAbandonPayment] are its Activity edges — the plan's
 * payment sheet opens from CaptainActivity, stamped "mopedu".
 */
fun NavGraphBuilder.mopeduCaptainScreen(
    onSignOut: () -> Unit,
    onOpenPayment: (CaptainPaymentRequest) -> Unit,
    onAbandonPayment: (CaptainPaymentRequest) -> Unit,
) {
    composable<MopeduCaptainRoute> {
        MopeduCaptainRoute(onSignOut = onSignOut, onOpenPayment = onOpenPayment, onAbandonPayment = onAbandonPayment)
    }
}
