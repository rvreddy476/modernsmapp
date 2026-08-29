// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph and navigation extensions that use them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.call.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import com.us.android.feature.call.ui.CallHistoryScreen
import com.us.android.feature.call.ui.CallScreen
import kotlinx.serialization.Serializable

/**
 * The one call surface (calling P0).
 *
 * Two entry modes share it:
 *  - OUTGOING: [peerId] is set — the screen requests permissions and rings.
 *  - ATTACH: [peerId] is null — the screen renders whatever the call state
 *    machine already holds (an incoming ring, a connecting or active call).
 *    This is the path notification taps and pushes land on.
 *
 * [peerName] is display-only, carried on the route so the FIRST frame has a
 * name (same rule as ChatThreadRoute); authorization never derives from it.
 */
@Serializable
data class CallRoute(
    val peerId: String? = null,
    val peerName: String = "",
    val video: Boolean = false,
    val conversationId: String? = null,
)

/** Past calls: missed, declined, completed with durations. */
@Serializable
data object CallHistoryRoute

fun NavGraphBuilder.callScreen(onBack: () -> Unit) {
    composable<CallRoute> {
        CallScreen(onBack = onBack)
    }
}

fun NavGraphBuilder.callHistoryScreen(onBack: () -> Unit) {
    composable<CallHistoryRoute> {
        CallHistoryScreen(onBack = onBack)
    }
}

fun NavController.navigateToOutgoingCall(
    peerId: String,
    peerName: String,
    video: Boolean,
    conversationId: String?,
) = navigate(
    CallRoute(
        peerId = peerId,
        peerName = peerName,
        video = video,
        conversationId = conversationId,
    ),
)

/** Attach to the live call state (incoming ring / ongoing call). */
fun NavController.navigateToCallSurface() = navigate(CallRoute())

fun NavController.navigateToCallHistory() = navigate(CallHistoryRoute)
