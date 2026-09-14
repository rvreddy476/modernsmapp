package com.us.android.feature.feast.checkout

import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.food.model.toRupeeText
import com.us.android.feature.feast.cart.BillCard
import com.us.android.feature.feast.ui.BottomAction
import com.us.android.feature.feast.ui.FeastCard
import com.us.android.feature.feast.ui.FeastScreen
import com.us.android.feature.feast.ui.InfoNote
import com.us.android.feature.feast.ui.LoadingPane
import com.us.android.feature.feast.ui.MessagePane
import com.us.android.feature.feast.ui.SectionLabel
import com.us.android.feature.feast.ui.Tone

/**
 * Checkout: where the food goes, how to pay, the server's bill, and the
 * payment's honest progress — "confirming" until the server says paid.
 */
@Composable
@Suppress("LongMethod", "LongParameterList", "CyclomaticComplexMethod")
fun FeastCheckoutScreen(
    onBack: () -> Unit,
    onOpenPayment: (FeastPaymentRequest) -> Unit,
    onAbandonPayment: (FeastPaymentRequest) -> Unit,
    onChangeAddress: () -> Unit,
    onTrackOrder: (String) -> Unit,
    onViewOrders: () -> Unit,
    onBrowse: () -> Unit,
    viewModel: FeastCheckoutViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.refreshAddress()
        onPauseOrDispose { }
    }

    // Open the sheet once per attempt: keyed on the request, not on every recomposition.
    val opening = (state as? FeastCheckoutState.OpeningPayment)?.request
    LaunchedEffect(opening) { opening?.let(onOpenPayment) }
    // Release the launcher's one slot if this screen goes away while the sheet is in flight.
    DisposableEffect(opening) {
        onDispose { opening?.let(onAbandonPayment) }
    }

    val ready = state as? FeastCheckoutState.Ready
    val inPayment = state is FeastCheckoutState.OpeningPayment || state is FeastCheckoutState.Confirming
    FeastScreen(
        title = "Checkout",
        // Leaving mid-payment would hide the only screen that says what happened.
        onBack = if (inPayment) null else onBack,
        bottomBar = {
            if (ready != null) {
                BottomAction(
                    label = ready.bill.total?.let { "Pay ${it.toRupeeText()} with ${ready.method.label}" } ?: "Pay",
                    onClick = viewModel::placeOrder,
                    enabled = ready.bill.payable && ready.address != null && !ready.placing,
                    loading = ready.placing,
                    summary = if (ready.address == null) "Add a delivery address to continue" else null,
                )
            }
        },
    ) { padding ->
        when (val s = state) {
            FeastCheckoutState.Loading -> LoadingPane()
            FeastCheckoutState.EmptyCart -> MessagePane(
                title = "Your cart is empty",
                body = "Add something from a restaurant to check out.",
                icon = UsIcons.ShoppingCart,
                primaryLabel = "Browse restaurants",
                onPrimary = onBrowse,
            )
            is FeastCheckoutState.Ready -> Column(
                modifier = Modifier
                    .fillMaxSize()
                    .verticalScroll(rememberScrollState())
                    .padding(top = padding.calculateTopPadding() + 8.dp, bottom = padding.calculateBottomPadding() + 24.dp),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                SectionLabel("Deliver to")
                FeastCard(onClick = onChangeAddress) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Icon(UsIcons.MapPin, contentDescription = null, tint = UsTheme.extended.accentSolid)
                        Spacer(Modifier.width(UsTheme.spacing.l))
                        Column(Modifier.weight(1f)) {
                            val address = s.address
                            if (address == null) {
                                Text("Add a delivery address", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                            } else {
                                Text(address.label ?: "Address", style = MaterialTheme.typography.titleSmall, color = UsTheme.extended.textPrimary)
                                Text(
                                    listOfNotNull(address.addressLine1, address.addressLine2, address.city).filter { it.isNotBlank() }.joinToString(", "),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = UsTheme.extended.textMuted,
                                )
                            }
                        }
                        Text("Change", style = MaterialTheme.typography.labelLarge, color = UsTheme.extended.accentSolid)
                    }
                }
                s.refusal?.let { FeastCard { InfoNote(it, tone = Tone.Danger) } }

                SectionLabel("Pay with")
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                    FeastPaymentMethod.entries.forEach { method ->
                        MethodChoice(
                            method = method,
                            selected = s.method == method,
                            onClick = { viewModel.selectMethod(method) },
                            modifier = Modifier.weight(1f),
                        )
                    }
                }

                SectionLabel("${s.restaurantName} · ${if (s.itemCount == 1) "1 item" else "${s.itemCount} items"}")
                BillCard(s.bill)
                InfoNote("Your order is confirmed only when the payment is — you'll see it here within moments.")
            }
            is FeastCheckoutState.OpeningPayment -> LoadingPane(label = "Opening ${s.request.method.label} payment…")
            is FeastCheckoutState.Confirming -> MessagePane(
                title = "Confirming your payment",
                body = "We're waiting for the bank to confirm order ${s.orderNumber}. Don't pay again — this usually takes a few seconds.",
                icon = UsIcons.Clock,
                extra = { LoadingPane(Modifier.size(64.dp).padding(top = 24.dp)) },
            )
            is FeastCheckoutState.Paid -> MessagePane(
                title = "Order placed",
                body = "Payment confirmed. Order ${s.orderNumber} is on its way to the restaurant.",
                icon = UsIcons.Check,
                iconTint = UsTheme.extended.statusSuccess,
                primaryLabel = "Track your order",
                onPrimary = { onTrackOrder(s.orderId) },
            )
            is FeastCheckoutState.PaymentFailed -> MessagePane(
                title = "Payment didn't go through",
                body = (s.reason?.let { "$it " } ?: "") +
                    "Your order ${s.orderNumber} is saved for a few minutes — you can try again without re-adding anything.",
                icon = UsIcons.CreditCard,
                iconTint = UsTheme.extended.statusDanger,
                primaryLabel = "Try again with ${s.method.label}",
                onPrimary = { viewModel.retryPayment() },
                secondaryLabel = "Pay with ${FeastPaymentMethod.entries.first { it != s.method }.label} instead",
                onSecondary = { viewModel.retryPayment(FeastPaymentMethod.entries.first { it != s.method }) },
            )
            is FeastCheckoutState.StillConfirming -> MessagePane(
                title = "Still confirming",
                body = "We haven't heard back about order ${s.orderNumber} yet. If money left your account it will be " +
                    "confirmed or refunded automatically — please don't pay twice.",
                icon = UsIcons.Clock,
                primaryLabel = "View your orders",
                onPrimary = onViewOrders,
                secondaryLabel = "Try paying again",
                onSecondary = { viewModel.retryPayment() },
            )
            is FeastCheckoutState.Refunding -> MessagePane(
                title = "Payment is being refunded",
                body = "Order ${s.orderNumber} couldn't go ahead, so the money you paid is on its way back to you.",
                icon = UsIcons.RotateCcw,
                primaryLabel = "View your orders",
                onPrimary = onViewOrders,
            )
            is FeastCheckoutState.Failed -> MessagePane(
                title = "Something went wrong",
                body = s.message,
                primaryLabel = if (s.retryable) "Try again" else null,
                onPrimary = viewModel::reload,
            )
        }
    }
}

@Composable
private fun MethodChoice(method: FeastPaymentMethod, selected: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(UsTheme.radii.large)
    val border = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle
    Row(
        modifier = modifier
            .clip(shape)
            .border(if (selected) 1.5.dp else 1.dp, border, shape)
            .clickable(onClick = onClick)
            .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.xl),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Icon(
            imageVector = if (method == FeastPaymentMethod.CARD) UsIcons.CreditCard else UsIcons.Phone,
            contentDescription = null,
            tint = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.textMuted,
            modifier = Modifier.size(20.dp),
        )
        Spacer(Modifier.width(UsTheme.spacing.m))
        Text(
            text = method.label,
            style = MaterialTheme.typography.titleSmall,
            fontWeight = if (selected) FontWeight.SemiBold else FontWeight.Normal,
            color = if (selected) UsTheme.extended.textPrimary else UsTheme.extended.textSecondary,
            modifier = Modifier.weight(1f),
        )
        if (selected) {
            Icon(UsIcons.Check, contentDescription = "Selected", tint = UsTheme.extended.accentSolid, modifier = Modifier.size(18.dp))
        } else {
            Spacer(Modifier.size(18.dp))
        }
    }
}
