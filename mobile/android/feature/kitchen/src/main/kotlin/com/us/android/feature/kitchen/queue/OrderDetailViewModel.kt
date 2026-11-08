package com.us.android.feature.kitchen.queue

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.KitchenOrderAction
import com.us.android.core.food.repository.KitchenRepository
import com.us.android.core.food.repository.code
import com.us.android.core.food.repository.outcomeUnknown
import com.us.android.feature.kitchen.navigation.requireArg
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.userMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

data class OrderDetailUiState(
    val loading: Boolean = true,
    val order: PartnerOrderDto? = null,
    val loadError: String? = null,
    val pickupCode: String = "",
    val pickupError: String? = null,
    val verifying: Boolean = false,
    /** Too many wrong codes for this assignment: the server takes no more, so neither does the field. */
    val pickupLocked: Boolean = false,
    val markingReady: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * One order: its items and bill, "mark ready", and the pickup handover.
 *
 * The order is read by id (`GET /partner/orders/:id`), which carries its items:
 * the restaurant's order list never does, and holds only the latest 100 orders.
 */
@HiltViewModel
class OrderDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val kitchen: KitchenRepository,
    updates: com.us.android.core.notifications.NotificationUpdates = com.us.android.core.notifications.NotificationUpdates(),
) : ViewModel() {

    private val orderId = savedStateHandle.requireArg("orderId")
    private var readyKey: String? = null

    private val _state = MutableStateFlow(OrderDetailUiState())
    val state: StateFlow<OrderDetailUiState> = _state.asStateFlow()

    init {
        load()
        viewModelScope.launch { updates.events.collect { if (it.startsWith("food_kitchen_") || it == "food_order_new") load() } }
    }

    fun load() {
        viewModelScope.launch {
            when (val result = kitchen.order(orderId)) {
                is FoodResult.Success -> _state.update {
                    it.copy(loading = false, order = result.value, loadError = null)
                }
                // A refresh that fails keeps the order already on screen; only a first load shows the error.
                is FoodResult.Failure -> _state.update { current ->
                    val shown = current.order != null
                    current.copy(loading = false, loadError = if (shown) null else loadMessage(result.error))
                }
            }
        }
    }

    fun onPickupCode(value: String) {
        _state.update { it.copy(pickupCode = value.filter(Char::isLetterOrDigit).take(MAX_CODE), pickupError = null) }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun markReady() {
        if (_state.value.markingReady) return
        _state.update { it.copy(markingReady = true) }
        viewModelScope.launch {
            val key = readyKey ?: UUID.randomUUID().toString().also { readyKey = it }
            when (val result = kitchen.transition(orderId, KitchenOrderAction.MARK_READY, key)) {
                is FoodResult.Success -> {
                    readyKey = null
                    _state.update { current ->
                        val updated = result.value
                        val keepItems = updated.items.isEmpty() && current.order != null
                        current.copy(
                            markingReady = false,
                            order = if (keepItems) updated.copy(items = current.order!!.items) else updated,
                            message = success("Marked ready. A rider is being found."),
                        )
                    }
                }
                is FoodResult.Failure -> {
                    if (!result.error.outcomeUnknown) readyKey = null
                    _state.update { it.copy(markingReady = false, message = result.error.asMessage()) }
                    load()
                }
            }
        }
    }

    fun verifyPickup() {
        if (_state.value.verifying || _state.value.pickupLocked) return
        val code = _state.value.pickupCode.trim()
        if (code.isEmpty()) {
            _state.update { it.copy(pickupError = "Enter the code the rider shows you") }
            return
        }
        _state.update { it.copy(verifying = true, pickupError = null) }
        viewModelScope.launch {
            when (val result = kitchen.verifyPickup(orderId, code)) {
                is FoodResult.Success -> {
                    _state.update {
                        it.copy(verifying = false, pickupCode = "", message = success("Handed over. The order is on its way."))
                    }
                    load()
                }
                is FoodResult.Failure -> _state.update { current ->
                    val fieldMessage = pickupMessage(result.error)
                    current.copy(
                        verifying = false,
                        pickupLocked = result.error.code == PICKUP_LOCKED,
                        pickupError = fieldMessage,
                        message = if (fieldMessage == null) result.error.asMessage() else null,
                    )
                }
            }
        }
    }

    private fun pickupMessage(error: FoodError): String? = when {
        error.code == PICKUP_LOCKED -> "Too many wrong codes for this order. Contact Feast support to hand it over."
        error.code == "FOOD_DELIVERY_ASSIGNMENT_NOT_READY" -> "No rider has this order yet. Wait for the rider to arrive."
        error.code == "PICKUP_VERIFY_FAILED" ||
            (error is FoodError.Unexpected && error.status == HTTP_BAD_REQUEST) -> "That code doesn't match. Ask the rider to read it again."
        else -> null
    }

    private fun loadMessage(error: FoodError): String =
        if (error is FoodError.NotFound) NOT_THIS_RESTAURANTS else error.userMessage()

    private companion object {
        const val MAX_CODE = 8
        const val HTTP_BAD_REQUEST = 400
        const val PICKUP_LOCKED = "FOOD_PICKUP_CODE_ATTEMPTS_EXCEEDED"
        const val NOT_THIS_RESTAURANTS = "This order isn't one of this restaurant's, or it no longer exists."
    }
}
