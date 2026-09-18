package com.us.android.feature.mopedu.captain

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.feature.mopedu.captain.ui.CaptainCard
import com.us.android.feature.mopedu.captain.ui.CaptainPill
import com.us.android.feature.mopedu.captain.ui.CaptainScreen
import com.us.android.feature.mopedu.captain.ui.CardHeading
import com.us.android.feature.mopedu.captain.ui.InfoNote
import com.us.android.feature.mopedu.captain.ui.LoadingPane
import com.us.android.feature.mopedu.captain.ui.PillTone
import com.us.android.feature.mopedu.captain.ui.SectionHeader

/**
 * The plans (2026-09-18): the trial in one tap, a paid plan by UPI or card
 * through the sheet. "Active" is rendered only from [PlanPhase.Activated],
 * which the ViewModel reaches from the server alone — never from the sheet
 * closing.
 */
@Composable
@Suppress("LongParameterList")
fun MopeduCaptainPlansScreen(
    state: CaptainUiState.Plans,
    onBack: () -> Unit,
    onStartTrial: () -> Unit,
    onChoosePlan: (planCode: String, method: PaymentMethod) -> Unit,
    onCheckAgain: () -> Unit,
    onDismissError: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val canChoose = when (state.phase) {
        PlanPhase.Choosing, is PlanPhase.Failed, is PlanPhase.StillConfirming -> !state.isLoading
        else -> false
    }
    CaptainScreen(
        title = when (state.reason) {
            PlansReason.FIRST_PLAN -> "Choose a plan"
            PlansReason.RENEWAL -> "Renew your plan"
            PlansReason.EXPIRED -> "Your plan has run out"
        },
        onBack = if (state.canGoBack) onBack else null,
        message = state.errorMessage?.let { UsMessage(it) },
        onDismissMessage = onDismissError,
        modifier = modifier,
    ) { padding ->
        if (state.isLoading && state.plans.isEmpty()) {
            LoadingPane(Modifier.padding(padding))
            return@CaptainScreen
        }
        LazyColumn(
            modifier = Modifier.fillMaxSize(),
            contentPadding = padding,
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item {
                CardHeading(
                    title = when (state.reason) {
                        PlansReason.FIRST_PLAN -> "You're verified. Pick a plan to start driving."
                        PlansReason.RENEWAL -> "Keep receiving rides without a break."
                        PlansReason.EXPIRED -> "Pay for a plan to go online again."
                    },
                    body = "Zero commission: you keep the whole fare. A plan sets your daily lead allowance.",
                    modifier = Modifier.padding(top = UsTheme.spacing.m),
                )
            }
            if (state.phase != PlanPhase.Choosing) {
                item { PaymentPhaseCard(state.phase, onRetry = onChoosePlan, onCheckAgain = onCheckAgain) }
            }
            state.subscription?.let { current ->
                item { CurrentPlanCard(current) }
            }
            item { SectionHeader("Plans") }
            if (state.plans.isEmpty()) {
                item { InfoNote("No plans are available right now. Try again in a moment.") }
            }
            items(state.plans, key = { it.code.ifBlank { it.id } }) { plan ->
                PlanCard(
                    plan = plan,
                    trialAvailable = state.trialAvailable,
                    enabled = canChoose,
                    onStartTrial = onStartTrial,
                    onChoosePlan = onChoosePlan,
                )
            }
            item {
                InfoNote(
                    "A plan becomes active only once Mopedu confirms the payment. If the sheet closes early, we keep checking with the server.",
                    modifier = Modifier.padding(bottom = UsTheme.spacing.xxxxl),
                )
            }
        }
    }
}

@Composable
private fun PaymentPhaseCard(phase: PlanPhase, onRetry: (String, PaymentMethod) -> Unit, onCheckAgain: () -> Unit) {
    when (phase) {
        PlanPhase.Choosing -> Unit
        is PlanPhase.Starting -> CaptainCard(highlighted = true) {
            CardHeading("Starting your plan", "Setting up the payment with Mopedu.", tone = PillTone.Accent)
            UsButton(text = "Please wait", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
        }
        is PlanPhase.OpeningSheet -> CaptainCard(highlighted = true) {
            CardHeading("Opening the payment sheet", "Complete the payment in the sheet that opens.", tone = PillTone.Accent)
            UsButton(text = "Opening", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
        }
        is PlanPhase.Confirming -> CaptainCard(highlighted = true) {
            CardHeading("Confirming your payment", "This takes a few seconds. Keep the app open.", tone = PillTone.Accent)
            UsButton(text = "Confirming (${phase.elapsedSeconds}s)", onClick = {}, loading = true, modifier = Modifier.fillMaxWidth())
        }
        is PlanPhase.StillConfirming -> CaptainCard(highlighted = true) {
            CardHeading(
                "Still confirming",
                "Mopedu hasn't confirmed the payment yet. If money left your account it will be applied — check again in a moment.",
                tone = PillTone.Warning,
            )
            UsSecondaryButton(text = "Check again", onClick = onCheckAgain, modifier = Modifier.fillMaxWidth())
        }
        is PlanPhase.Failed -> CaptainCard(highlighted = true) {
            CardHeading("Payment didn't go through", phase.reason ?: "Nothing was charged.", tone = PillTone.Danger)
            val planCode = phase.planCode
            if (phase.retryable && planCode != null) {
                UsButton(
                    text = "Try again by ${phase.method.displayName}",
                    onClick = { onRetry(planCode, phase.method) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        }
        PlanPhase.Activated -> CaptainCard(highlighted = true) {
            CardHeading("Your plan is active", "Mopedu confirmed it. Taking you to the console.", tone = PillTone.Positive)
        }
    }
}

@Composable
private fun CurrentPlanCard(subscription: PartnerSubscription) {
    CaptainCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(
                title = subscription.planName.ifBlank { subscription.planCode },
                body = "Until ${subscription.expiresAt.toDisplayDate()}",
                modifier = Modifier.weight(1f),
            )
            CaptainPill(
                subscription.status.replace('_', ' '),
                if (subscription.isUsable) PillTone.Positive else PillTone.Danger,
            )
        }
    }
}

@Composable
@Suppress("LongParameterList")
private fun PlanCard(
    plan: SubscriptionPlan,
    trialAvailable: Boolean,
    enabled: Boolean,
    onStartTrial: () -> Unit,
    onChoosePlan: (String, PaymentMethod) -> Unit,
) {
    CaptainCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CardHeading(plan.name.ifBlank { plan.code }, plan.description.ifBlank { null }, modifier = Modifier.weight(1f))
            Text(
                text = if (plan.price.isZero) "Free" else plan.price.formattedINR,
                style = MaterialTheme.typography.titleMedium,
                fontWeight = FontWeight.Bold,
                color = UsTheme.extended.accentSolid,
            )
        }
        Text(
            text = listOfNotNull(
                plan.billingPeriodDays.takeIf { it > 0 }?.let { "$it days" } ?: plan.billingCycle.replace('_', ' ').ifBlank { null },
                "Daily leads: ${plan.dailyLeadCap ?: "unlimited"}",
            ).joinToString(" · "),
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textMuted,
        )
        if (plan.isTrial) {
            if (trialAvailable) {
                UsButton(text = "Start free trial", onClick = onStartTrial, enabled = enabled, modifier = Modifier.fillMaxWidth())
            } else {
                InfoNote("The free trial is granted once per account, and this account has had it.")
            }
        } else {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                UsSecondaryButton(
                    text = "Pay by UPI",
                    onClick = { onChoosePlan(plan.code, PaymentMethod.UPI) },
                    enabled = enabled,
                    modifier = Modifier.weight(1f),
                )
                UsButton(
                    text = "Pay by card",
                    onClick = { onChoosePlan(plan.code, PaymentMethod.CARD) },
                    enabled = enabled,
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

/** `2026-09-25T00:00:00Z` as `25 Sep 2026`; the raw text when it cannot be parsed. */
internal fun String.toDisplayDate(): String {
    val date = runCatching { java.time.OffsetDateTime.parse(this).toLocalDate() }.getOrNull()
        ?: runCatching { java.time.Instant.parse(this).atZone(java.time.ZoneId.systemDefault()).toLocalDate() }.getOrNull()
        ?: return this
    return date.format(java.time.format.DateTimeFormatter.ofPattern("d MMM yyyy", java.util.Locale.ENGLISH))
}
