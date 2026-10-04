package com.us.android.feature.doorsteppro.account

import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.feature.doorsteppro.data.*
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.ProCard
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import java.math.BigDecimal
import javax.inject.Inject

/** Currency is submitted as integer paise; never a floating-point estimate. */
internal fun pricePaise(raw: String): Long? = runCatching {
    if (!Regex("\\d{1,7}(\\.\\d{1,2})?").matches(raw.trim())) return null
    BigDecimal(raw.trim()).movePointRight(2).longValueExact().takeIf { it in 100..10_000_000 }
}.getOrNull()

data class PricesState(val items: List<ProServicePricingDto> = emptyList(), val busy: Boolean = false, val message: String? = null)

@HiltViewModel
class PricesViewModel @Inject constructor(private val repository: DoorstepProRepository) : ViewModel() {
    val state = MutableStateFlow(PricesState())
    init { reload() }
    fun reload() { viewModelScope.launch {
        state.value = state.value.copy(busy = true)
        when (val result = repository.prices()) {
            is ProResult.Success -> state.value = state.value.copy(items = result.value, busy = false)
            is ProResult.Failure -> state.value = state.value.copy(busy = false, message = "Your prices could not load. Please try again.")
        }
    } }
    private fun update(call: suspend () -> ProResult<*>, success: String) {
        if (state.value.busy) return
        viewModelScope.launch {
            state.value = state.value.copy(busy = true, message = null)
            when (val result = call()) {
                is ProResult.Success -> { state.value = state.value.copy(busy = false, message = success); reload() }
                is ProResult.Failure -> state.value = state.value.copy(busy = false, message = (result.error as? ProError.Refused)?.message ?: "That change could not be saved. Please try again.")
            }
        }
    }
    fun submit(service: ProServicePricingDto, item: ProPriceItemDto, raw: String) {
        val paise = pricePaise(raw) ?: run { state.value = state.value.copy(message = "Enter a price above ₹0 with no more than two decimal places."); return }
        update({ repository.submitPrice(ProPriceRequest(service.serviceId, item.itemKind, item.itemId, paise)) }, "Submitted for admin review. Your live price is unchanged until approval.")
    }
    fun withdraw(id: String) = update({ repository.withdrawPrice(id) }, "Pending price withdrawn.")
    fun sameDay(service: ProServicePricingDto) = update({ repository.sameDay(service.serviceId, !service.sameDay) }, "Same-day preference saved. Go on duty to receive ASAP jobs.")
}

@Composable
fun PricesCard(viewModel: PricesViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProCard {
        CardHeading("Your service prices", "Set GST-inclusive charges. Every change needs admin approval.")
        state.message?.let { Text(it) }
        if (state.items.isEmpty()) UsSecondaryButton(text = if (state.busy) "Loading…" else "Reload prices", onClick = viewModel::reload, enabled = !state.busy)
        state.items.forEach { service ->
            Text(service.serviceName)
            Text(if (service.bookable) "Available to book" else "Not bookable yet · ${service.skillStatus}")
            Text("Accept same-day jobs")
            Switch(checked = service.sameDay, enabled = !state.busy && service.skillStatus != "revoked", onCheckedChange = { viewModel.sameDay(service) })
            service.items.forEach { item ->
                var price by rememberSaveable(service.serviceId, item.itemId) { mutableStateOf("") }
                Text(item.name)
                item.approved?.let { Text("Live: ₹${BigDecimal(it.pricePaise).movePointLeft(2)} · ${item.unit.replace('_', ' ')}") }
                item.rejected?.reason?.let { Text("Previous submission: $it") }
                val pending = item.pending
                if (pending != null) {
                    Text("₹${BigDecimal(pending.pricePaise).movePointLeft(2)} awaiting review")
                    UsSecondaryButton(text = "Withdraw pending price", enabled = !state.busy, onClick = { viewModel.withdraw(pending.id) })
                } else {
                    UsTextField(value = price, onValueChange = { price = it }, label = "Your price (₹)", keyboardType = KeyboardType.Decimal, enabled = !state.busy && service.skillStatus != "revoked", modifier = Modifier.fillMaxWidth())
                    UsButton(text = "Submit for review", enabled = !state.busy && pricePaise(price) != null && service.skillStatus != "revoked", onClick = { viewModel.submit(service, item, price) })
                }
            }
        }
    }
}
