package com.us.android.feature.memberships.mine

import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsMessageHost
import com.us.android.core.designsystem.component.UsMessageType
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.MyMembership
import com.us.android.core.memberships.data.rupees
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.memberships.ui.MembersOnlyLevelSheet
import com.us.android.core.ui.UsEmptyState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.memberships.navigation.MembershipsDestinations

/**
 * One membership: who and which level, the price, when access ends, and
 * Renew / Change level / Cancel. The member's notices land here.
 */
@Suppress("LongMethod") // The page and its two dialogs: one screen, one composable.
@Composable
fun MembershipDetailScreen(
    destinations: MembershipsDestinations,
    viewModel: MembershipDetailViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val activity = LocalActivity.current
    LaunchedEffect(state.pay) {
        val opening = state.pay as? MembershipPayState.OpeningPayment ?: return@LaunchedEffect
        if (activity != null) viewModel.openSheet(activity, opening.request)
    }
    DisposableEffect(Unit) { onDispose { viewModel.abandon() } }
    var confirmCancel by rememberSaveable { mutableStateOf(false) }
    var choosingLevel by rememberSaveable { mutableStateOf(false) }

    UsScaffold(topBar = { UsTopBar(title = "Membership", onBack = destinations.onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).testTag("membership_detail")) {
            val membership = state.membership
            when {
                state.loading && membership == null -> UsLoadingState(label = "Loading membership")
                state.loadError != null && membership == null ->
                    UsErrorState(message = state.loadError.orEmpty(), onRetry = viewModel::refresh)
                state.missing || membership == null -> UsEmptyState(
                    title = "Membership not found",
                    detail = "It may have ended more than 30 days ago. Join again from the creator's profile.",
                )
                else -> Column(
                    modifier = Modifier.fillMaxSize().verticalScroll(
                        rememberScrollState()
                    ).padding(vertical = UsTheme.spacing.l),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    Summary(membership = membership, creatorName = state.creatorName, onCreator = {
                        destinations.onOpenCreator(membership.creatorId)
                    })
                    PayStatus(state = state, viewModel = viewModel)
                    Actions(
                        state = state,
                        membership = membership,
                        onRenew = viewModel::renew,
                        onChange = { choosingLevel = true },
                        onCancel = { confirmCancel = true },
                    )
                }
            }
            val message = remember(
                state.message
            ) { state.message?.let { UsMessage(text = it, type = UsMessageType.Info) } }
            UsMessageHost(
                message = message,
                onDismiss = viewModel::dismissMessage,
                modifier = Modifier.align(Alignment.BottomCenter)
            )
        }
    }

    if (confirmCancel) {
        AlertDialog(
            onDismissRequest = { confirmCancel = false },
            containerColor = UsTheme.extended.bgSheet,
            title = { Text("Cancel membership?") },
            text = {
                Text(
                    "You keep access until " +
                        "${membershipDate(state.membership?.accessUntil).ifBlank { "the end of the month" }}. " +
                        "Nothing more is charged, and the month already paid isn't refunded.",
                )
            },
            confirmButton = {
                TextButton(onClick = {
                    confirmCancel = false
                    viewModel.cancel()
                }, modifier = Modifier.testTag("membership_cancel_confirm")) {
                    Text("Cancel membership", color = UsTheme.extended.statusDanger)
                }
            },
            dismissButton = {
                TextButton(
                    onClick = { confirmCancel = false }
                ) { Text("Keep it", color = UsTheme.extended.textPrimary) }
            },
        )
    }
    if (choosingLevel) {
        MembersOnlyLevelSheet(
            levels = state.otherLevels,
            selectedId = null,
            onSelect = { id ->
                choosingLevel = false
                if (id != null) viewModel.changeLevel(id)
            },
            onDismiss = { choosingLevel = false },
            title = "Change level",
            detail = "Moving up is charged now, for what is left of this month. " +
                "Moving down starts next month; nothing is charged.",
            includeEveryone = false,
            levelLabel = { it.name },
        )
    }
}

@Composable
private fun Summary(membership: MyMembership, creatorName: String, onCreator: () -> Unit) {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth(), onClick = onCreator) {
        Text(
            text = creatorName.ifBlank { "Creator" },
            style = MaterialTheme.typography.titleLarge,
            fontWeight = FontWeight.Bold
        )
        Text(
            text = "${membership.levelName.ifBlank { "Membership" }} · ${rupees(membership.pricePaise)} a month",
            style = MaterialTheme.typography.bodyLarge,
        )
        membership.perks.forEach { perk ->
            Text(text = "• $perk", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textBody)
        }
        membership.pendingDowngrade?.let { down ->
            Text(
                text = pendingDowngradeLine(down),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.testTag("membership_pending_downgrade"),
            )
        }
        Text(
            text = membership.status.label,
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.accentSolid
        )
        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            Text(text = "Paid until", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            Text(text = membershipDate(membership.currentPeriodEnd), style = MaterialTheme.typography.bodySmall)
        }
        if (membership.accessUntil != null) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Text(
                    text = "Access until",
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted
                )
                Text(text = membershipDate(membership.accessUntil), style = MaterialTheme.typography.bodySmall)
            }
        }
        Text(
            text = "Renewal is by reminder: we'll tell you before your month ends, " +
                "and nothing is charged automatically.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
    }
}

@Composable
private fun PayStatus(state: MembershipDetailUiState, viewModel: MembershipDetailViewModel) {
    val (text, action) = when (val pay = state.pay) {
        MembershipPayState.Idle, MembershipPayState.Submitting -> return
        is MembershipPayState.OpeningPayment, MembershipPayState.InSheet -> "Complete the payment." to null
        is MembershipPayState.Confirming -> "Confirming your payment…" to null
        MembershipPayState.Paid -> "Payment confirmed. Thank you!" to ("Done" to viewModel::donePayment)
        is MembershipPayState.Applied -> (state.message ?: "Done.") to ("Done" to viewModel::donePayment)
        is MembershipPayState.Failed ->
            pay.message to if (pay.retryable) {
                "Pay again" to viewModel::retryPayment
            } else {
                "Close" to viewModel::donePayment
            }
        is MembershipPayState.Refused -> pay.message to ("Close" to viewModel::donePayment)
        MembershipPayState.Refunding ->
            "This payment couldn't be applied, so it's being refunded." to ("Close" to viewModel::donePayment)
        MembershipPayState.StillConfirming ->
            "Still confirming. If money left your account, it will show here once confirmed." to
                ("Check again" to viewModel::retryPayment)
    }
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("membership_pay_status")) {
        Text(text = text, style = MaterialTheme.typography.bodyMedium)
        action?.let { (label, onClick) -> UsSecondaryButton(text = label, onClick = onClick, compact = true) }
    }
}

@Composable
private fun Actions(
    state: MembershipDetailUiState,
    membership: MyMembership,
    onRenew: () -> Unit,
    onChange: () -> Unit,
    onCancel: () -> Unit,
) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        if (membership.renewAvailable) {
            UsButton(
                text = "Renew for ${rupees(membership.pricePaise)}",
                onClick = onRenew,
                enabled = !state.busy,
                loading = state.pay is MembershipPayState.Submitting,
                modifier = Modifier.fillMaxWidth().testTag("membership_renew"),
            )
        }
        if (membership.canChangeLevel && state.otherLevels.isNotEmpty()) {
            UsSecondaryButton(
                text = "Change level",
                onClick = onChange,
                enabled = !state.busy,
                modifier = Modifier.fillMaxWidth().testTag("membership_change"),
            )
        }
        if (membership.canCancel) {
            UsSecondaryButton(
                text = if (state.cancelling) "Cancelling…" else "Cancel membership",
                onClick = onCancel,
                enabled = !state.busy,
                modifier = Modifier.fillMaxWidth().testTag("membership_cancel"),
            )
        }
        if (membership.status == MembershipStatus.CancelledAtPeriodEnd) {
            Text(
                text = "Renewing turns your membership back on.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
    }
}
