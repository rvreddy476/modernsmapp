package com.us.android.feature.commerce.checkout

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.ui.UsLoadingState
import com.us.android.feature.commerce.ui.CommerceNotice
import com.us.android.feature.commerce.ui.CommerceProgressLine
import com.us.android.feature.commerce.ui.MStorePageBar
import com.us.android.feature.commerce.ui.PriceBreakdownCard
import com.us.android.feature.commerce.ui.pressScale

/**
 * Checkout, payment handoff, and confirmation.
 *
 * The screen the whole money boundary exists to serve. Three of its states
 * are load-bearing rather than cosmetic:
 *
 *  * [CheckoutUiState.PriceChanged] BLOCKS. The customer sees old → new and
 *    must accept before anything is retried.
 *  * [CheckoutUiState.AwaitingConfirmation] never says "paid". A PSP redirect
 *    is evidence, not proof; the server marks an order paid only on a
 *    signature-verified webhook, so this state says "confirming" and polls.
 *  * [CheckoutUiState.Expired] must not promise delivery. The reservation
 *    lapsed and the stock went back on sale; a late capture is refunded.
 *
 * @param onOpenPaymentSheet hands off to the PSP. The caller supplies this
 *   because the SDK integration lives in `:app` — a feature module must not
 *   know which provider is wired.
 */
@Composable
@Suppress("LongMethod", "LongParameterList", "CyclomaticComplexMethod")
fun CheckoutScreen(
    addressId: String,
    addressSummary: String,
    onBack: () -> Unit,
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit,
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit,
    onEditBag: () -> Unit,
    onChangeAddress: () -> Unit,
    onViewOrder: (orderId: String) -> Unit,
    modifier: Modifier = Modifier,
    viewModel: CheckoutViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()

    LaunchedEffect(addressId) {
        // C3-LB-2: no subtotal is passed in. The server prices the bag and
        // the screen renders exactly what it returns.
        viewModel.prepare(addressId, addressSummary)
    }

    // The order exists and holds stock; the PSP sheet is the next step. Doing
    // this in an effect rather than a button keeps the handoff automatic —
    // the customer already pressed "Place order".
    //
    // Keyed on the ORDER ID rather than on `state`, so a recomposition while
    // the sheet is open cannot reopen it. Keying on `state` would relaunch on
    // every state object identity change.
    val opening = state as? CheckoutUiState.OpeningPayment
    LaunchedEffect(opening?.attempt) {
        val o = opening ?: return@LaunchedEffect
        onOpenPaymentSheet(o.attempt, o.orderNumber)
    }

    // C3-LB-4. If this screen goes away while a sheet is in flight, release
    // the launcher's single slot. Without this a buyer who backs out mid-sheet
    // would find every later checkout refused with "a payment is already in
    // progress" until the process restarted.
    //
    // Correctness does not depend on this — an outcome is matched to its
    // attempt, so a late callback can never settle a later order — but a
    // wedged launcher is still a broken checkout.
    val inFlight = opening?.attempt
    DisposableEffect(inFlight) {
        onDispose { inFlight?.let(onAbandonPaymentSheet) }
    }

    // The sheet's outcome comes back on the handoff bus, because the SDK
    // reports to the Activity and not to whoever opened it. Every ending —
    // success, failure, cancellation — leads to the same place: ask the
    // server. A1/R-3, a client callback is evidence and never proof.
    LaunchedEffect(Unit) {
        viewModel.observePaymentHandoff()
    }

    UsScaffold(
        modifier = modifier,
        topBar = { MStorePageBar(title = "Checkout", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            when (val s = state) {
                CheckoutUiState.Loading -> UsLoadingState(label = "Working out delivery")

                is CheckoutUiState.Ready -> ReadyBody(
                    state = s,
                    onSelectMethod = viewModel::selectMethod,
                    onPlaceOrder = viewModel::placeOrder,
                    onChangeAddress = onChangeAddress,
                )

                is CheckoutUiState.PriceChanged -> PriceChangedBody(
                    state = s,
                    onAccept = viewModel::acknowledgePriceChange,
                    onEditBag = onEditBag,
                )

                is CheckoutUiState.OutOfStock -> OutOfStockBody(state = s, onEditBag = onEditBag)

                CheckoutUiState.QuoteStale -> BlockingBody(
                    title = "Let's recalculate delivery",
                    detail = "Your bag or address changed after we worked out delivery. " +
                        "We'll do it again so you're charged the right amount.",
                    primaryLabel = "Recalculate",
                    onPrimary = viewModel::requoteAfterStaleQuote,
                    secondaryLabel = "Edit bag",
                    onSecondary = onEditBag,
                )

                is CheckoutUiState.NotServiceable -> BlockingBody(
                    title = "We can't deliver there yet",
                    detail = s.reason ?: "We don't deliver to this address at the moment.",
                    primaryLabel = "Change address",
                    onPrimary = onChangeAddress,
                )

                is CheckoutUiState.OpeningPayment -> CenteredProgress("Opening payment…")

                is CheckoutUiState.AwaitingConfirmation -> AwaitingBody(s, onViewOrder)

                is CheckoutUiState.Paid -> BlockingBody(
                    title = "Order confirmed",
                    detail = "Order ${s.orderNumber} is confirmed. " +
                        "We'll let you know when it ships.",
                    primaryLabel = "View order",
                    onPrimary = { onViewOrder(s.orderId) },
                )

                is CheckoutUiState.PaymentFailed -> BlockingBody(
                    title = "Payment didn't go through",
                    detail = "Order ${s.orderNumber} is still held for you. " +
                        "You can try paying again.",
                    primaryLabel = "Try payment again",
                    // C3-LB-4: the ViewModel mints a NEW attempt, so the
                    // failed one's late callback cannot settle this retry.
                    onPrimary = viewModel::retryPayment,
                    secondaryLabel = "View order",
                    onSecondary = { onViewOrder(s.orderId) },
                )

                is CheckoutUiState.Expired -> BlockingBody(
                    // LB-22: the hold lapsed and the stock went back on sale.
                    // This copy must not promise delivery — a late capture is
                    // refunded automatically, not fulfilled.
                    title = "This order expired",
                    detail = "We held your items while you paid, but the hold ran out and " +
                        "they've gone back on sale. If any money was taken, it's " +
                        "refunded automatically. Please start a new order.",
                    primaryLabel = "Back to bag",
                    onPrimary = onEditBag,
                    secondaryLabel = "View order",
                    onSecondary = { onViewOrder(s.orderId) },
                )

                CheckoutUiState.RetryWithNewAttempt -> BlockingBody(
                    // M-7: the key stood for a different request. Starting
                    // fresh is what stops a changed address silently shipping
                    // against the earlier attempt's order.
                    title = "Let's start again",
                    detail = "Something changed since you started. We'll begin a fresh " +
                        "attempt so nothing is ordered twice.",
                    primaryLabel = "Start again",
                    onPrimary = viewModel::acknowledgePriceChange,
                )

                is CheckoutUiState.Failed -> BlockingBody(
                    title = "That didn't work",
                    detail = s.message,
                    primaryLabel = if (s.retryable) "Try again" else "Back to bag",
                    onPrimary = if (s.retryable) viewModel::placeOrder else onEditBag,
                )
            }
        }
    }
}

@Composable
private fun ReadyBody(
    state: CheckoutUiState.Ready,
    onSelectMethod: (PaymentMethod) -> Unit,
    onPlaceOrder: () -> Unit,
    onChangeAddress: () -> Unit,
) {
    SectionTitle("Deliver to")
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            text = state.addressSummary.ifBlank { "Selected address" },
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        // The app's inline row action is a pill, not a text link — the same
        // control the notification rows use beside a name. Outlined, because
        // the ember belongs to "Place order".
        UsPillButton(text = "Change", onClick = onChangeAddress, filled = false)
    }

    SectionTitle("Payment method")
    PaymentMethod.entries.forEach { method ->
        MethodRow(
            method = method,
            selected = method == state.paymentMethod,
            onClick = { onSelectMethod(method) },
        )
    }

    SectionTitle("Order total")
    PriceBreakdownCard(breakdown = state.breakdown)

    Text(
        text = "You'll be asked to pay ${state.breakdown.total.formatWithSymbol()}. " +
            "Your order is confirmed only once we've verified the payment.",
        style = MaterialTheme.typography.labelSmall,
        color = UsTheme.extended.textSecondary,
    )

    UsButton(
        text = "Place order",
        onClick = onPlaceOrder,
        loading = state.placing,
        enabled = !state.placing,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.xxl),
    )
}

/**
 * The blocking price-change state.
 *
 * Old → new, per line, with an explicit accept. §7-J5: acknowledging is what
 * turns the new price into a decision the customer made rather than a
 * surprise on their statement.
 */
@Composable
private fun PriceChangedBody(
    state: CheckoutUiState.PriceChanged,
    onAccept: () -> Unit,
    onEditBag: () -> Unit,
) {
    SectionTitle("The price changed")
    Text(
        text = "Some prices moved while you were shopping. Please check the new " +
            "total before we continue.",
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textSecondary,
    )

    state.lines.forEach { line ->
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = line.was.formatWithSymbol(),
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
                textDecoration = TextDecoration.LineThrough,
            )
            Text(
                text = "→",
                style = MaterialTheme.typography.bodyMedium,
                color = UsTheme.extended.textSecondary,
            )
            Text(
                text = line.now.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }
    }

    state.newTotal?.let { total ->
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text(
                text = "New total",
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = total.formatWithSymbol(),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
            )
        }
    }

    UsButton(
        text = "Accept and continue",
        onClick = onAccept,
        modifier = Modifier.fillMaxWidth(),
    )
    UsSecondaryButton(
        text = "Back to bag",
        onClick = onEditBag,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.xxl),
    )
}

@Composable
private fun OutOfStockBody(state: CheckoutUiState.OutOfStock, onEditBag: () -> Unit) {
    SectionTitle("Some items ran out")
    state.lines.forEach { line ->
        CommerceNotice(
            text = if (line.available > 0) {
                "${line.title}: only ${line.available} left, you asked for ${line.requested}."
            } else {
                "${line.title} is out of stock."
            },
        )
    }
    UsButton(
        text = "Update bag",
        onClick = onEditBag,
        modifier = Modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.xxl),
    )
}

/**
 * Waiting on the server to confirm the payment.
 *
 * A1/R-3. The copy is careful: the sheet closing tells us the SDK finished a
 * flow, and nothing more. Saying "Payment successful" here would be asserting
 * something no one has verified, and the provider can still fail to capture
 * after the redirect.
 */
@Composable
private fun AwaitingBody(
    state: CheckoutUiState.AwaitingConfirmation,
    onViewOrder: (String) -> Unit,
) {
    CenteredProgress("Confirming your payment…")
    Text(
        text = "We're checking with your bank. This usually takes a few seconds. " +
            "Don't pay again — order ${state.orderNumber} is already placed.",
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textSecondary,
    )
    if (state.elapsedSeconds >= SLOW_CONFIRMATION_SECONDS) {
        Text(
            text = "This is taking longer than usual. You can safely leave this " +
                "screen — we'll update the order when the payment confirms.",
            style = MaterialTheme.typography.bodySmall,
            color = UsTheme.extended.textSecondary,
        )
        UsSecondaryButton(
            text = "View order",
            onClick = { onViewOrder(state.orderId) },
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun BlockingBody(
    title: String,
    detail: String,
    primaryLabel: String,
    onPrimary: () -> Unit,
    secondaryLabel: String? = null,
    onSecondary: (() -> Unit)? = null,
) {
    SectionTitle(title)
    Text(
        text = detail,
        style = MaterialTheme.typography.bodyMedium,
        color = UsTheme.extended.textSecondary,
    )
    UsButton(
        text = primaryLabel,
        onClick = onPrimary,
        modifier = Modifier.fillMaxWidth(),
    )
    if (secondaryLabel != null && onSecondary != null) {
        UsSecondaryButton(
            text = secondaryLabel,
            onClick = onSecondary,
            modifier = Modifier
                .fillMaxWidth()
                .padding(bottom = UsTheme.spacing.xxl),
        )
    }
}

@Composable
private fun MethodRow(method: PaymentMethod, selected: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.medium))
            // Selected is WHITE. On the one screen where the ember means
            // "pay", a chosen payment method must not wear it too.
            .border(
                width = if (selected) SELECTED_BORDER else UNSELECTED_BORDER,
                color = if (selected) Color.White else UsTheme.extended.borderSubtle,
                shape = RoundedCornerShape(UsTheme.radii.medium),
            )
            .background(UsTheme.extended.bgCard)
            .pressScale(onClick = onClick, role = Role.RadioButton)
            .padding(UsTheme.spacing.l),
    ) {
        Text(
            text = method.label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.titleMedium,
        color = UsTheme.extended.textPrimary,
        modifier = Modifier.padding(top = UsTheme.spacing.m),
    )
}

/**
 * What we are waiting on, over the shop's ember line.
 *
 * The line rather than a spinner: this is the money screen, and the same
 * indicator the rest of commerce uses is one fewer thing that looks borrowed
 * from a different app at the moment a customer is deciding to trust it.
 */
@Composable
private fun CenteredProgress(label: String) {
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.xl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m),
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodyMedium,
            color = UsTheme.extended.textPrimary,
        )
        CommerceProgressLine(contentDescription = label)
    }
}

private const val SLOW_CONFIRMATION_SECONDS = 20
private val SELECTED_BORDER = 2.dp
private val UNSELECTED_BORDER = 1.dp
