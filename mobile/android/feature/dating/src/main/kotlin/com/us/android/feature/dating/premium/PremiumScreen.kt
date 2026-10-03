package com.us.android.feature.dating.premium

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.network.PremiumMeDto
import com.us.android.feature.dating.network.PremiumProductDto
import com.us.android.feature.dating.ui.DatingCard
import com.us.android.feature.dating.ui.DatingScreen
import com.us.android.feature.dating.ui.InfoNote
import com.us.android.feature.dating.ui.LoadingPane
import com.us.android.feature.dating.ui.MessagePane
import com.us.android.feature.dating.ui.Pill
import com.us.android.feature.dating.ui.Tone
import com.us.android.feature.dating.ui.listPadding
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import java.util.Locale

/** Premium passes and Boost. [onOpenPayment] and [onAbandonPayment] are `:app`'s Activity edges. */
@Composable
fun PremiumScreen(
    onBack: () -> Unit,
    onOpenPayment: (DatingPaymentRequest) -> Unit,
    onAbandonPayment: (DatingPaymentRequest) -> Unit,
    viewModel: PremiumViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(state) {
        (state as? PremiumState.OpeningPayment)?.let { onOpenPayment(it.request) }
    }
    DisposableEffect(Unit) {
        onDispose {
            // Releases the launcher's one in-flight slot if the buyer leaves mid-sheet.
            val attempt = viewModel.activeAttempt()
            val opening = viewModel.state.value as? PremiumState.OpeningPayment
            if (attempt != null && opening != null && opening.request.attempt == attempt) onAbandonPayment(opening.request)
        }
    }

    DatingScreen(title = "Premium", onBack = onBack) { padding ->
        when (val s = state) {
            PremiumState.Loading, is PremiumState.OpeningPayment -> LoadingPane()
            PremiumState.Unavailable -> MessagePane(
                title = DatingCopy.PREMIUM_UNAVAILABLE,
                body = "Passes and Boost are coming soon.",
                icon = UsIcons.Clock,
                primaryLabel = "Back",
                onPrimary = onBack,
            )
            is PremiumState.Failed -> MessagePane(title = "Premium didn't load", body = s.message, primaryLabel = "Try again", onPrimary = viewModel::retry)
            is PremiumState.Confirming -> LoadingPane(label = "Confirming your payment…")
            is PremiumState.StillConfirming -> MessagePane(
                title = "Still confirming",
                body = "Your payment for ${s.productName} hasn't been confirmed yet. If money left your account, it will show here once it's confirmed.",
                icon = UsIcons.Clock,
                primaryLabel = "Check again",
                onPrimary = viewModel::retry,
                secondaryLabel = "Done",
                onSecondary = viewModel::done,
            )
            is PremiumState.Paid -> MessagePane(
                title = "You're all set",
                body = "${s.productName} is active.",
                icon = UsIcons.Check,
                iconTint = UsTheme.extended.statusSuccess,
                primaryLabel = "Done",
                onPrimary = viewModel::done,
            )
            is PremiumState.PaymentFailed -> MessagePane(
                title = "Payment didn't go through",
                body = s.reason ?: "Nothing was charged for this attempt. You can try again.",
                icon = UsIcons.Info,
                iconTint = UsTheme.extended.statusDanger,
                primaryLabel = "Try again",
                onPrimary = viewModel::retry,
                secondaryLabel = "Back",
                onSecondary = viewModel::done,
            )
            is PremiumState.Refunding -> MessagePane(
                title = "Your money is being returned",
                body = "This payment for ${s.productName} is being refunded.",
                icon = UsIcons.RotateCcw,
                primaryLabel = "Done",
                onPrimary = viewModel::done,
            )
            is PremiumState.Ready -> LazyColumn(contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                s.notice?.let { item { InfoNote(it, tone = Tone.Warning) } }
                s.me?.let { me ->
                    item {
                        DatingCard {
                            val pass = me.pass
                            if (me.isPremium && pass?.active == true) {
                                Pill("Premium", Tone.Accent)
                                Text(
                                    displayDate(pass.expiresAt)?.let { "Active until $it" } ?: "Active",
                                    style = MaterialTheme.typography.titleMedium,
                                    color = UsTheme.extended.textPrimary,
                                )
                                FeatureList(activeFeatureLabels(me))
                            } else {
                                Text("You don't have a pass right now.", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
                            }
                            balancesLine(me.boostBalance, me.superSparkBalance)?.let {
                                Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                            }
                        }
                    }
                }
                items(s.products, key = { it.id }) { product -> ProductCard(product, buying = s.buying == product.id, enabled = s.buying == null, onBuy = { viewModel.buy(product.id) }) }
                item { InfoNote("Passes don't renew automatically. We'll remind you before one ends.") }
            }
        }
    }
}

@Composable
private fun ProductCard(product: PremiumProductDto, buying: Boolean, enabled: Boolean, onBuy: () -> Unit) {
    DatingCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            androidx.compose.foundation.layout.Column(Modifier.weight(1f)) {
                Text(product.name, style = MaterialTheme.typography.titleMedium, color = UsTheme.extended.textPrimary)
                packLabel(product)?.let {
                    Text(it, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textSecondary)
                }
                Text(rupees(product.amountMinor, product.currency), style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textSecondary)
            }
            UsPillButton(text = "Buy", onClick = onBuy, enabled = enabled, busy = buying)
        }
        FeatureList(featureLabels(product.features))
    }
}

/** One line per thing a pass unlocks, each with a tick. Nothing at all for an empty list. */
@Composable
private fun FeatureList(labels: List<String>) {
    labels.forEach { label ->
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
            Icon(UsIcons.Check, contentDescription = null, tint = UsTheme.extended.accentSolid, modifier = Modifier.size(14.dp))
            Text(label, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
    }
}

/** "5 Super Sparks" for a Super Spark pack; null for anything else, or a pack with no quantity. */
fun packLabel(product: PremiumProductDto): String? {
    if (product.kind != KIND_SUPER_SPARK || product.quantity <= 0) return null
    return if (product.quantity == 1) "1 Super Spark" else "${product.quantity} Super Sparks"
}

/** "Boosts: 1 · Super Sparks: 3" — each only when there is some; null when there is neither. */
fun balancesLine(boosts: Int, superSparks: Int): String? =
    listOfNotNull(
        "Boosts: $boosts".takeIf { boosts > 0 },
        "Super Sparks: $superSparks".takeIf { superSparks > 0 },
    ).joinToString(" · ").ifEmpty { null }

private const val KIND_SUPER_SPARK = "super_spark"

/**
 * Our words for what a pass unlocks (mechanic M10). The server lists a
 * mechanic's feature only while that mechanic is on; a code this table does
 * not know is null, and is left off the screen rather than shown raw.
 */
fun featureLabel(feature: String): String? = when (feature) {
    "match_extend" -> "Extend matches"
    "daily_boost" -> "A daily Boost"
    "more_daily_cards" -> "More people on Pulse each day"
    "unlimited_rewinds" -> "Undo as many passes as you like"
    "more_super_sparks" -> "More Super Sparks"
    "see_who_sparked" -> "See who sparked you"
    "advanced_filters" -> "More filters"
    "travel_mode" -> "Browse another city"
    "read_receipts" -> "Read receipts"
    else -> null
}

/** Every known feature's label, in the server's order, each once; unknown codes are left out. */
fun featureLabels(features: List<String>): List<String> = features.mapNotNull(::featureLabel).distinct()

/** What the caller's active pass unlocks right now, from `GET /premium/me`. */
fun activeFeatureLabels(me: PremiumMeDto): List<String> =
    featureLabels(me.entitlements.filter { it.active }.map { it.feature })

/** The catalogue's amount as rupees: 39900 → "₹399", 4950 → "₹49.50". */
fun rupees(amountMinor: Long, currency: String = "INR"): String {
    val symbol = if (currency.equals("INR", ignoreCase = true)) "₹" else "$currency "
    val whole = amountMinor / PAISE_PER_RUPEE
    val paise = amountMinor % PAISE_PER_RUPEE
    return if (paise == 0L) "$symbol$whole" else "$symbol$whole.${paise.toString().padStart(2, '0')}"
}

/** "16 Oct 2026" from an RFC 3339 timestamp; null when it cannot be read. */
fun displayDate(iso: String?): String? = iso?.let {
    runCatching { OffsetDateTime.parse(it).format(DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH)) }.getOrNull()
}

private const val PAISE_PER_RUPEE = 100L
