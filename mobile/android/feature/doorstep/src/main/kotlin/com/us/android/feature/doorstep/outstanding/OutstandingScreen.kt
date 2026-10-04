package com.us.android.feature.doorstep.outstanding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.domain.BillRules
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.payment.BillPayState
import com.us.android.feature.doorstep.payment.BillPayment
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.doorstep.ui.DoorstepCard
import com.us.android.feature.doorstep.ui.DoorstepScreen
import com.us.android.feature.doorstep.ui.InfoNote
import com.us.android.feature.doorstep.ui.LoadingPane
import com.us.android.feature.doorstep.ui.MessagePane
import com.us.android.feature.doorstep.ui.MoneyRow
import com.us.android.feature.doorstep.ui.Tone
import com.us.android.feature.doorstep.ui.listPadding
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class OutstandingUiState(
    val loading: Boolean = true,
    val outstanding: OutstandingDto? = null,
    val error: String? = null,
)

/** Unpaid extras from earlier visits. While any is open, new bookings are refused (DOORSTEP_OUTSTANDING_DUE). */
@HiltViewModel
class OutstandingViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    handoff: PaymentHandoff,
    payments: PaymentCoordinator,
) : ViewModel() {

    private val _state = MutableStateFlow(OutstandingUiState())
    val state: StateFlow<OutstandingUiState> = _state.asStateFlow()

    val bill = BillPayment(viewModelScope, repository, handoff, payments, savedStateHandle, onSettled = { load() })

    fun load() {
        viewModelScope.launch {
            when (val result = repository.outstanding()) {
                is DoorstepResult.Success -> _state.update { it.copy(loading = false, outstanding = result.value, error = null) }
                is DoorstepResult.Failure -> _state.update { it.copy(loading = false, error = result.error.userMessage()) }
            }
        }
    }
}

@Composable
fun OutstandingScreen(
    onBack: () -> Unit,
    onOpenPayment: (DoorstepPaymentRequest) -> Unit,
    onAbandonPayment: (DoorstepPaymentRequest) -> Unit,
    onOpenBooking: (bookingId: String) -> Unit,
    viewModel: OutstandingViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val pay by viewModel.bill.state.collectAsStateWithLifecycle()
    LifecycleResumeEffect(viewModel) {
        viewModel.load()
        onPauseOrDispose { }
    }
    val opening = pay as? BillPayState.Opening
    LaunchedEffect(opening?.request?.attempt) { opening?.let { onOpenPayment(it.request) } }
    DisposableEffect(opening?.request) {
        val request = opening?.request
        onDispose { if (request != null) onAbandonPayment(request) }
    }

    DoorstepScreen(title = "Pending dues", onBack = onBack) { padding ->
        val dues = state.outstanding
        when {
            state.loading && dues == null -> LoadingPane()
            dues == null -> MessagePane(
                title = "Couldn't load your dues",
                body = state.error.orEmpty(),
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
            )
            dues.bills.none { BillRules.payable(it) } -> MessagePane(
                title = "All settled",
                body = "You have no pending dues. You can book again.",
                icon = UsIcons.Check,
                iconTint = UsTheme.extended.statusSuccess,
                primaryLabel = "Done",
                onPrimary = onBack,
            )
            else -> LazyColumn(
                modifier = Modifier.fillMaxSize(),
                contentPadding = listPadding(padding),
                verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            ) {
                item {
                    DoorstepCard {
                        MoneyRow("Total due", Paise(dues.totalPaise), emphasise = true)
                        InfoNote("Extras you approved during a visit and didn't pay at the end. Pay them to book again.")
                    }
                }
                when (val p = pay) {
                    is BillPayState.Confirming, is BillPayState.Starting -> item { InfoNote("Confirming your payment with the bank…", tone = Tone.Accent) }
                    is BillPayState.Failed -> item { InfoNote(p.reason ?: "The payment didn't go through. Try again.", tone = Tone.Danger) }
                    is BillPayState.StillConfirming -> item {
                        InfoNote("Still confirming. If money left your account, this clears on its own.", tone = Tone.Warning)
                    }
                    is BillPayState.Refunding -> item { InfoNote("That payment is being refunded.", tone = Tone.Warning) }
                    else -> Unit
                }
                items(dues.bills.filter { BillRules.payable(it) }, key = { it.id }) { bill ->
                    DoorstepCard(onClick = { onOpenBooking(bill.bookingId) }) {
                        MoneyRow("Extras bill", Paise(bill.amountPaise), emphasise = true)
                        MoneyRow("Of which GST", Paise(bill.taxPaise), muted = true)
                        Text("Tap to see the visit", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                        UsPillButton(
                            text = "Pay now",
                            onClick = { viewModel.bill.pay(bill.bookingId, bill.id) },
                            busy = pay is BillPayState.Starting || pay is BillPayState.Confirming,
                        )
                    }
                }
            }
        }
    }
}
