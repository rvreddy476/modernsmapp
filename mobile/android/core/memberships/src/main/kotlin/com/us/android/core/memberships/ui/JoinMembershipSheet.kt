package com.us.android.core.memberships.ui

import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState

/**
 * The Join sheet (paid memberships, 2026-10-23): [creatorName]'s levels —
 * name, price a month, perks — and Join, which pays through Razorpay and
 * unlocks only when the server confirms the payment. [requiredLevelId] is the
 * level the post that opened it needs: levels below it are marked as not
 * opening it, and the first that does is picked.
 *
 * [onUnlocked] is called once the server said paid (or that the viewer was
 * already a member), so the host re-reads the post it locked.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun JoinMembershipSheet(
    creatorId: String,
    creatorName: String,
    onDismiss: () -> Unit,
    onUnlocked: () -> Unit,
    requiredLevelId: String? = null,
    /** The level's price as the post carried it (`tier_required_price_paise`), for which levels open it. */
    requiredPricePaise: Long? = null,
    viewModel: JoinMembershipViewModel = hiltViewModel(key = "join:$creatorId"),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val activity = LocalActivity.current
    LaunchedEffect(creatorId, requiredLevelId) { viewModel.bind(creatorId, requiredLevelId, requiredPricePaise) }
    LaunchedEffect(state.pay) {
        val opening = state.pay as? MembershipPayState.OpeningPayment ?: return@LaunchedEffect
        if (activity != null) viewModel.openSheet(activity, opening.request)
    }
    DisposableEffect(Unit) { onDispose { viewModel.abandon() } }

    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    val finish = {
        val unlocked = state.pay is MembershipPayState.Paid ||
            (state.pay as? MembershipPayState.Refused)?.code == JoinMembershipViewModel.ALREADY_SUBSCRIBED
        viewModel.done()
        if (unlocked) onUnlocked()
        onDismiss()
    }
    ModalBottomSheet(
        onDismissRequest = finish,
        sheetState = sheetState,
        containerColor = UsTheme.extended.bgSheet,
        contentColor = UsTheme.extended.textPrimary,
        scrimColor = UsTheme.extended.scrim,
        modifier = Modifier.testTag("join_membership_sheet:$creatorId"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .navigationBarsPadding(),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            Text(
                text = "Join ${creatorName.ifBlank { "this creator" }}",
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "Members see members-only posts. You pay each month yourself — nothing renews on its own.",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textMuted,
            )
            JoinBody(state = state, viewModel = viewModel, onFinish = finish)
            Spacer(Modifier.height(UsTheme.spacing.xxl))
        }
    }
}

@Composable
private fun JoinBody(state: JoinUiState, viewModel: JoinMembershipViewModel, onFinish: () -> Unit) {
    when {
        state.loading -> Box(
            Modifier.fillMaxWidth().heightIn(min = BODY_MIN)
        ) { UsLoadingState(label = "Loading levels") }
        state.loadError != null -> Box(Modifier.fillMaxWidth().heightIn(min = BODY_MIN)) {
            UsErrorState(message = state.loadError, onRetry = viewModel::retryLoad)
        }
        state.unavailable != null -> Outcome(
            title = "Memberships unavailable",
            body = state.unavailable,
            action = "Close",
            onAction = onFinish,
        )
        else -> PayBody(state = state, viewModel = viewModel, onFinish = onFinish)
    }
}

@Suppress("CyclomaticComplexMethod") // One branch per payment state: a lookup table, not logic.
@Composable
private fun PayBody(state: JoinUiState, viewModel: JoinMembershipViewModel, onFinish: () -> Unit) {
    val levelName = state.selected?.name.orEmpty()
    when (val pay = state.pay) {
        MembershipPayState.Idle, MembershipPayState.Submitting, is MembershipPayState.Refused -> {
            state.levels.forEach { level ->
                LevelOption(
                    level = level,
                    selected = level.id == state.selectedId,
                    opensThisPost = state.opensThisPost(level),
                    showsPostFit = state.requiredLevelId != null,
                    onClick = { viewModel.select(level.id) },
                )
            }
            if (pay is MembershipPayState.Refused) {
                Notice(text = pay.message)
                if (pay.code == JoinMembershipViewModel.ALREADY_SUBSCRIBED) {
                    UsButton(text = "Done", onClick = onFinish, modifier = Modifier.fillMaxWidth())
                    return
                }
            }
            val selected = state.selected
            UsButton(
                text = selected?.let { "Join for ${it.priceLabel}" } ?: "Pick a level",
                onClick = viewModel::join,
                enabled = state.canJoin,
                loading = pay is MembershipPayState.Submitting,
                modifier = Modifier.fillMaxWidth().testTag("join_membership_pay"),
            )
            Text(
                text = "Price includes GST. Paid through Razorpay.",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        is MembershipPayState.OpeningPayment, MembershipPayState.InSheet ->
            Waiting(text = "Complete the payment to join.")
        is MembershipPayState.Confirming -> Waiting(text = "Confirming your payment…")
        MembershipPayState.Paid -> Outcome(
            title = "You're a member",
            body = "Your ${levelName.ifBlank { "membership" }} is active. Members-only posts are unlocked.",
            action = "Done",
            onAction = onFinish,
            testTag = "join_membership_paid",
        )
        is MembershipPayState.Applied -> Outcome(
            title = "Done",
            body = pay.message,
            action = "Done",
            onAction = onFinish
        )
        is MembershipPayState.Failed -> Outcome(
            title = "Payment didn't go through",
            body = pay.message,
            action = if (pay.retryable) "Pay again" else "Close",
            onAction = if (pay.retryable) viewModel::retryPayment else onFinish,
            secondary = if (pay.retryable) "Not now" else null,
            onSecondary = onFinish,
        )
        MembershipPayState.Refunding -> Outcome(
            title = "Your payment is being refunded",
            body = "It couldn't be applied to a membership, so the money is on its way back to you.",
            action = "Close",
            onAction = onFinish,
        )
        MembershipPayState.StillConfirming -> Outcome(
            title = "Still confirming",
            body = "Your payment hasn't been confirmed yet. " +
                "If money left your account, the posts unlock as soon as it is.",
            action = "Check again",
            onAction = viewModel::retryPayment,
            secondary = "Close",
            onSecondary = onFinish,
        )
    }
}

/** One level: its name and price, its perks, and whether it opens the post that asked. */
@Composable
private fun LevelOption(
    level: MembershipLevel,
    selected: Boolean,
    opensThisPost: Boolean,
    showsPostFit: Boolean,
    onClick: () -> Unit,
) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .clip(shape)
            .border(
                BorderStroke(
                    if (selected) SELECTED_BORDER else 1.dp,
                    if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
                ),
                shape,
            )
            .background(UsTheme.extended.bgCardSolid, shape)
            .clickable(role = Role.RadioButton, onClick = onClick)
            .semantics { this.selected = selected }
            .padding(UsTheme.spacing.xl)
            .testTag("join_level:${level.id}"),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = level.name,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = level.priceLabel,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.textPrimary,
            )
        }
        if (showsPostFit) {
            Text(
                text = if (opensThisPost) "Includes this post" else "Doesn't include this post",
                style = MaterialTheme.typography.labelMedium,
                color = if (opensThisPost) UsTheme.extended.statusSuccess else UsTheme.extended.textMuted,
            )
        }
        level.perks.forEach { perk ->
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), verticalAlignment = Alignment.Top) {
                Icon(
                    imageVector = UsIcons.Check,
                    contentDescription = null,
                    tint = UsTheme.extended.accentSolid,
                    modifier = Modifier.size(PERK_ICON).padding(top = UsTheme.spacing.xs),
                )
                Text(text = perk, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textBody)
            }
        }
    }
}

@Composable
private fun Waiting(text: String) {
    Row(
        modifier = Modifier.fillMaxWidth().heightIn(min = BODY_MIN).testTag("join_membership_waiting"),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        CircularProgressIndicator(
            modifier = Modifier.size(SPINNER),
            color = UsTheme.extended.accentSolid,
            strokeWidth = 2.dp
        )
        Text(text = text, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
    }
}

@Composable
private fun Notice(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.statusDanger,
        modifier = Modifier.testTag("join_membership_notice"),
    )
}

@Composable
private fun Outcome(
    title: String,
    body: String,
    action: String,
    onAction: () -> Unit,
    secondary: String? = null,
    onSecondary: () -> Unit = {},
    testTag: String = "join_membership_outcome",
) {
    Column(
        modifier = Modifier.fillMaxWidth().testTag(testTag),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Box(
            modifier = Modifier.size(BADGE).clip(CircleShape).background(UsTheme.extended.fillSubtle),
            contentAlignment = Alignment.Center,
        ) {
            Icon(imageVector = UsIcons.Award, contentDescription = null, tint = UsTheme.extended.accentSolid)
        }
        Text(text = title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        Text(text = body, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        UsButton(text = action, onClick = onAction, modifier = Modifier.fillMaxWidth())
        if (secondary != null) {
            UsSecondaryButton(
                text = secondary,
                onClick = onSecondary,
                modifier = Modifier.fillMaxWidth()
            )
        }
    }
}

private val BODY_MIN = 120.dp
private val SELECTED_BORDER = 2.dp
private val PERK_ICON = 18.dp
private val SPINNER = 24.dp
private val BADGE = 48.dp
