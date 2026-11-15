package com.us.android.feature.premium.ui

import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsSurfaceCard
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.premium.data.MoneyLine
import com.us.android.feature.premium.data.PremiumInvoice
import com.us.android.feature.premium.data.PremiumPlan
import com.us.android.feature.premium.data.PremiumStatus
import com.us.android.feature.premium.data.PremiumWords
import com.us.android.feature.premium.data.StatusTone
import com.us.android.feature.premium.data.amountText
import com.us.android.feature.premium.data.breakdownText
import com.us.android.feature.premium.data.canBuy
import com.us.android.feature.premium.data.invoiceDate
import com.us.android.feature.premium.data.invoiceFileName
import com.us.android.feature.premium.data.label
import com.us.android.feature.premium.data.lines
import com.us.android.feature.premium.data.paidWords
import com.us.android.feature.premium.data.planLength
import com.us.android.feature.premium.data.priceText
import com.us.android.feature.premium.data.reversed
import com.us.android.feature.premium.data.rupees
import com.us.android.feature.premium.data.title
import com.us.android.feature.premium.data.words
import com.us.android.feature.premium.payment.PurchaseState
import com.us.android.feature.premium.payment.busy

/**
 * Momentum Premium (2026-11-13), the web's /premium in the app's design: the
 * viewer's status, two pass cards ("₹149 + GST" and the server's breakdown),
 * Buy → the confirm step (Price, GST, Total) → Razorpay → the pass poll →
 * "Premium is active until …", Post on Echo and Download invoice; then the
 * buyer's tax invoices and credit notes.
 *
 * Documents are saved through the system's "Save to": the app streams the
 * authenticated response into the picked file and keeps no copy.
 */
@Composable
fun PremiumScreen(
    onBack: () -> Unit,
    onPostOnEcho: () -> Unit,
    viewModel: PremiumViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val activity = LocalActivity.current
    LaunchedEffect(state.purchase) {
        val opening = state.purchase as? PurchaseState.OpeningPayment ?: return@LaunchedEffect
        if (activity != null) viewModel.openSheet(activity, opening.request)
    }
    DisposableEffect(Unit) { onDispose { viewModel.abandon() } }
    val saveTo = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument(INVOICE_MIME)) { uri ->
        viewModel.saveTo(uri)
    }
    val download: (PremiumInvoiceTarget) -> Unit = { target ->
        if (viewModel.startDownload(target.id)) saveTo.launch(invoiceFileName(target.number ?: target.id))
    }

    UsScaffold(topBar = { UsTopBar(title = PremiumWords.TITLE, onBack = onBack) }) { padding ->
        Box(modifier = Modifier.fillMaxSize().padding(padding).testTag("premium_screen")) {
            when {
                state.off -> OffCard()
                state.loading -> UsLoadingState(label = "Loading")
                else -> Column(
                    modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState())
                        .padding(vertical = UsTheme.spacing.l),
                    verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
                ) {
                    Text(
                        text = PremiumWords.SUBTITLE,
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textSecondary,
                    )
                    PurchaseBanner(
                        state = state,
                        onPay = viewModel::pay,
                        onCheckAgain = viewModel::checkAgain,
                        onRetry = viewModel::retry,
                        onReset = viewModel::resetPurchase,
                        onPostOnEcho = onPostOnEcho,
                        onDownload = download,
                    )
                    StatusCard(status = state.status, error = state.statusError)
                    // Selling off (15 Nov 2026): no passes drawn; status and invoices stay.
                    if (state.canBuy) Plans(state = state, onBuy = viewModel::buy)
                    PerksCard()
                    InvoicesSection(
                        invoices = state.invoices,
                        download = state.download,
                        onDownload = download,
                    )
                    DownloadNote(state.download, onDismiss = viewModel::dismissDownloadMessage)
                }
            }
        }
    }
}

/** A document to save: its id and, when known, its number (the file's name). */
data class PremiumInvoiceTarget(val id: String, val number: String?)

@Composable
private fun OffCard() {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.l).testTag("premium_off")) {
        Text(
            text = PremiumWords.NOT_ENABLED,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )
        Text(
            text = PremiumWords.NOT_ENABLED_DETAIL,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.s),
        )
    }
}

@Composable
private fun StatusCard(status: PremiumStatus?, error: String?) {
    if (error != null) {
        UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("premium_status:error")) {
            Text(text = error, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
        }
        return
    }
    val w = status?.words() ?: return
    val tint = when (w.tone) {
        StatusTone.ACTIVE -> UsTheme.extended.statusSuccess
        StatusTone.GRACE -> UsTheme.extended.statusWarning
        StatusTone.NONE -> UsTheme.extended.textPrimary
    }
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("premium_status:${w.tone.name.lowercase()}")) {
        Text(
            text = w.title,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
            color = tint,
        )
        Text(
            text = w.detail,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.padding(top = UsTheme.spacing.xs),
        )
    }
}

@Composable
private fun Plans(state: PremiumUiState, onBuy: (PremiumPlan) -> Unit) {
    Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
        Text(text = PremiumWords.CHOOSE, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
        val error = state.plansError
        if (error != null) {
            Text(text = error, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
        } else {
            val busy = state.purchase.busy
            state.plans.forEach { plan ->
                val pending = busy && (state.purchase as? PurchaseState.Starting)?.plan == plan.code
                PlanCard(plan = plan, busy = busy, pending = pending, onBuy = { onBuy(plan) })
            }
        }
        Text(
            text = PremiumWords.NO_AUTO_RENEW,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
    }
}

@Composable
private fun PlanCard(plan: PremiumPlan, busy: Boolean, pending: Boolean, onBuy: () -> Unit) {
    val buyable = plan.canBuy()
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("premium_plan:${plan.code}")) {
        Text(
            text = planLength(plan.durationDays),
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textSecondary,
        )
        Text(text = plan.title(), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        if (buyable) {
            Row(
                verticalAlignment = Alignment.Bottom,
                horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            ) {
                Text(
                    text = plan.priceText(),
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.Bold,
                )
                Text(
                    text = "+ GST",
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textSecondary,
                )
            }
            Text(
                text = plan.breakdownText(),
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.testTag("premium_breakdown:${plan.code}"),
            )
        } else {
            Text(
                text = plan.priceText(),
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textSecondary,
                modifier = Modifier.padding(top = UsTheme.spacing.m),
            )
        }
        UsButton(
            text = if (buyable) "Buy ${planLength(plan.durationDays)}" else PremiumWords.NOT_AVAILABLE,
            onClick = onBuy,
            enabled = buyable && !busy,
            loading = pending,
            modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("premium_buy:${plan.code}"),
        )
    }
}

@Composable
private fun PerksCard() {
    UsSurfaceCard(modifier = Modifier.fillMaxWidth()) {
        Text(
            text = PremiumWords.PERKS_HEADING,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = FontWeight.SemiBold,
        )
        Text(
            text = "✓ ${PremiumWords.PERK_ECHO}",
            style = MaterialTheme.typography.bodyMedium,
            modifier = Modifier.padding(top = UsTheme.spacing.s),
        )
        Text(
            text = PremiumWords.PERKS_FREE,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.padding(top = UsTheme.spacing.s),
        )
    }
}

@Composable
private fun MoneyLines(lines: List<MoneyLine>, tag: String) {
    Column(modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m).testTag("premium_lines:$tag")) {
        lines.forEachIndexed { i, line ->
            val last = i == lines.lastIndex && lines.size > 1
            if (last) HorizontalDivider(modifier = Modifier.padding(vertical = UsTheme.spacing.xs))
            Row(modifier = Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                Text(
                    text = line.label,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = if (last) FontWeight.SemiBold else FontWeight.Normal,
                )
                Text(
                    text = line.value,
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = if (last) FontWeight.SemiBold else FontWeight.Normal,
                )
            }
        }
    }
}

/** The purchase's own state, above the passes. Nothing shows while idle. */
// One `when` over the purchase's states, each a few lines of layout.
@Suppress("LongMethod", "LongParameterList", "CyclomaticComplexMethod")
@Composable
private fun PurchaseBanner(
    state: PremiumUiState,
    onPay: () -> Unit,
    onCheckAgain: () -> Unit,
    onRetry: () -> Unit,
    onReset: () -> Unit,
    onPostOnEcho: () -> Unit,
    onDownload: (PremiumInvoiceTarget) -> Unit,
) {
    when (val p = state.purchase) {
        PurchaseState.Idle, PurchaseState.Unavailable -> Unit
        is PurchaseState.Review -> UsSurfaceCard(modifier = bannerModifier("review")) {
            Text(
                text = PremiumWords.CONFIRM_HEADING,
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
            )
            MoneyLines(p.checkout.lines(), tag = "checkout")
            UsButton(
                text = "Pay ${rupees(p.checkout.amountPaise)}",
                onClick = onPay,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("premium_pay"),
            )
            TextButton(onClick = onReset, modifier = Modifier.fillMaxWidth()) { Text("Cancel") }
        }
        is PurchaseState.Starting, is PurchaseState.OpeningPayment, PurchaseState.InSheet ->
            UsSurfaceCard(modifier = bannerModifier("opening")) {
                Text(
                    text = if (p is PurchaseState.Starting) PremiumWords.OPENING else PremiumWords.IN_SHEET,
                    style = MaterialTheme.typography.bodyMedium,
                )
            }
        is PurchaseState.Confirming -> UsSurfaceCard(modifier = bannerModifier("confirming")) {
            Text(text = PremiumWords.CONFIRMING, style = MaterialTheme.typography.bodyMedium)
            TextButton(onClick = onReset, modifier = Modifier.fillMaxWidth()) { Text("Close") }
        }
        is PurchaseState.Paid -> UsSurfaceCard(modifier = bannerModifier("paid")) {
            Text(
                text = paidWords(p.endsAt),
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.statusSuccess,
            )
            p.receipt?.let { MoneyLines(it.lines(), tag = "receipt") }
            PaidInvoiceLine(paid = p, state = state, onDownload = onDownload)
            UsButton(
                text = "Post on Echo",
                onClick = onPostOnEcho,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("premium_post_on_echo"),
            )
            TextButton(onClick = onReset, modifier = Modifier.fillMaxWidth()) { Text("Done") }
        }
        PurchaseState.StillConfirming -> UsSurfaceCard(modifier = bannerModifier("still_confirming")) {
            Text(text = PremiumWords.STILL_CONFIRMING, style = MaterialTheme.typography.bodyMedium)
            UsButton(
                text = "Check again",
                onClick = onCheckAgain,
                modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l).testTag("premium_check_again"),
            )
            TextButton(onClick = onReset, modifier = Modifier.fillMaxWidth()) { Text("Close") }
        }
        is PurchaseState.Failed -> UsSurfaceCard(modifier = bannerModifier("failed")) {
            Text(text = p.message, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.error)
            if (p.plan != null) {
                UsButton(
                    text = "Try again",
                    onClick = onRetry,
                    modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.l),
                )
            }
            TextButton(onClick = onReset, modifier = Modifier.fillMaxWidth()) { Text("Close") }
        }
    }
}

/**
 * The success view's invoice: Download once the pass names its invoice;
 * until then "ready in a moment", then a pointer to the Invoices list.
 */
@Composable
private fun PaidInvoiceLine(
    paid: PurchaseState.Paid,
    state: PremiumUiState,
    onDownload: (PremiumInvoiceTarget) -> Unit,
) {
    val invoiceId = paid.invoiceId
    if (invoiceId == null) {
        Text(
            text = if (paid.invoiceGaveUp) PremiumWords.INVOICE_LATER else PremiumWords.INVOICE_PREPARING,
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
            modifier = Modifier.padding(top = UsTheme.spacing.m).testTag("premium_invoice:waiting"),
        )
        return
    }
    val number = state.invoices.firstOrNull { it.id == invoiceId }?.number
    UsSecondaryButton(
        text = PremiumWords.DOWNLOAD_INVOICE,
        onClick = { onDownload(PremiumInvoiceTarget(invoiceId, number)) },
        enabled = state.download.pendingId != invoiceId,
        modifier = Modifier.fillMaxWidth().padding(top = UsTheme.spacing.m).testTag("premium_invoice:download"),
    )
}

/**
 * The buyer's tax invoices and credit notes, newest first. Hidden when there
 * are none. A credit note is a refund: its amount carries a minus and it
 * names the invoice it reverses when that invoice is in the list.
 */
@Composable
private fun InvoicesSection(
    invoices: List<PremiumInvoice>,
    download: InvoiceDownloadState,
    onDownload: (PremiumInvoiceTarget) -> Unit,
) {
    if (invoices.isEmpty()) return
    UsSurfaceCard(modifier = Modifier.fillMaxWidth().testTag("premium_invoices")) {
        Text(
            text = PremiumWords.INVOICES,
            style = MaterialTheme.typography.titleMedium,
            fontWeight = FontWeight.SemiBold,
        )
        invoices.forEachIndexed { i, inv ->
            if (i > 0) HorizontalDivider()
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(vertical = UsTheme.spacing.m)
                    .testTag("premium_invoice_row:${inv.id}"),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(modifier = Modifier.weight(1f)) {
                    Text(text = inv.number, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Medium)
                    inv.reversed(invoices)?.let { original ->
                        Text(
                            text = "Reverses ${original.number}",
                            style = MaterialTheme.typography.bodySmall,
                            color = UsTheme.extended.textSecondary,
                        )
                    }
                    Text(
                        text = "${invoiceDate(inv.date)} · ${inv.kind.label()}",
                        style = MaterialTheme.typography.bodySmall,
                        color = UsTheme.extended.textSecondary,
                    )
                }
                Text(
                    text = inv.amountText(),
                    style = MaterialTheme.typography.bodyMedium,
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.m),
                )
                TextButton(
                    onClick = { onDownload(PremiumInvoiceTarget(inv.id, inv.number)) },
                    enabled = download.pendingId != inv.id,
                    modifier = Modifier.testTag("premium_invoice_download:${inv.id}"),
                ) { Text(PremiumWords.DOWNLOAD_ROW) }
            }
        }
    }
}

@Composable
private fun DownloadNote(download: InvoiceDownloadState, onDismiss: () -> Unit) {
    val message = download.message ?: return
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(
            text = message,
            style = MaterialTheme.typography.bodySmall,
            color = if (download.saved) UsTheme.extended.statusSuccess else MaterialTheme.colorScheme.error,
            modifier = Modifier.weight(1f).testTag("premium_download_message"),
        )
        TextButton(onClick = onDismiss) { Text("OK") }
    }
}

/** The document's type: printable HTML (the server ships no PDF renderer). */
private const val INVOICE_MIME = "application/pdf"

/** The purchase banner card, tagged by its state for UI tests. */
private fun bannerModifier(state: String): Modifier = Modifier.fillMaxWidth().testTag("premium_purchase:$state")
