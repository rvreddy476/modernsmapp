package com.us.android.feature.rider.job

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.location.RiderDuty
import com.us.android.feature.rider.ui.asMessage
import com.us.android.feature.rider.ui.error
import com.us.android.feature.rider.ui.success
import com.us.android.feature.rider.ui.userMessage
import com.us.android.feature.rider.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

data class ActiveJobUiState(
    val loading: Boolean = true,
    val assignment: DeliveryAssignmentDto? = null,
    val actions: JobActions? = null,
    val restaurant: NavTarget? = null,
    val customer: NavTarget? = null,
    val busy: Boolean = false,
    val code: String = "",
    val codeError: String? = null,
    val verifying: Boolean = false,
    /** Five wrong codes: the field is closed and support must help. */
    val codeLocked: Boolean = false,
    val confirmingRelease: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * The job in hand: confirm → ride to the restaurant (pickup code for the
 * kitchen) → the kitchen verifies pickup → ride to the customer → enter the
 * customer's delivery code.
 *
 * Re-reads the assignment every [POLL_MILLIS] so the kitchen's pickup
 * verification moves the screen on without a tap. Every step carries an
 * Idempotency-Key kept only while the outcome is unknown (a network failure).
 */
@HiltViewModel
class ActiveJobViewModel @Inject constructor(
    private val rider: RiderRepository,
    private val duty: RiderDuty,
    private val locations: JobLocations,
) : ViewModel() {

    private val _state = MutableStateFlow(ActiveJobUiState())
    val state: StateFlow<ActiveJobUiState> = _state.asStateFlow()

    private val finished = Channel<Unit>(Channel.CONFLATED)
    val finishedEvents: Flow<Unit> = finished.receiveAsFlow()

    private val idempotencyKeys = HashMap<Pair<String, RiderAssignmentStep>, String>()

    init {
        viewModelScope.launch {
            while (true) {
                load()
                delay(POLL_MILLIS)
            }
        }
    }

    fun perform(step: RiderAssignmentStep) {
        val assignment = _state.value.assignment ?: return
        if (_state.value.busy) return
        _state.update { it.copy(busy = true, confirmingRelease = false) }
        viewModelScope.launch {
            val key = idempotencyKeys.getOrPut(assignment.id to step) { UUID.randomUUID().toString() }
            val result = rider.step(assignment.id, step, key)
            if (!(result is FoodResult.Failure && result.error is FoodError.Network)) idempotencyKeys.remove(assignment.id to step)
            when (result) {
                is FoodResult.Success -> {
                    if (step == RiderAssignmentStep.REJECT) {
                        duty.setOnJob(false)
                        finished.trySend(Unit)
                    } else {
                        apply(result.value)
                    }
                    _state.update { it.copy(busy = false) }
                }
                is FoodResult.Failure -> {
                    _state.update { it.copy(busy = false, message = result.error.asMessage()) }
                    load()
                }
            }
        }
    }

    fun askRelease() = _state.update { it.copy(confirmingRelease = true) }

    fun cancelRelease() = _state.update { it.copy(confirmingRelease = false) }

    fun onCode(value: String) = _state.update { it.copy(code = value.filter(Char::isDigit).take(CODE_LENGTH), codeError = null) }

    fun verifyCode() {
        val assignment = _state.value.assignment ?: return
        val code = _state.value.code
        if (code.length != CODE_LENGTH || _state.value.verifying || _state.value.codeLocked) {
            if (code.length != CODE_LENGTH) _state.update { it.copy(codeError = "Enter the $CODE_LENGTH-digit code the customer shows you") }
            return
        }
        _state.update { it.copy(verifying = true) }
        viewModelScope.launch {
            val outcome = DeliveryCodeOutcome.from(rider.verifyDelivery(assignment.id, code))
            _state.update { it.copy(verifying = false) }
            when (outcome) {
                DeliveryCodeOutcome.Delivered -> {
                    duty.setOnJob(false)
                    _state.update { it.copy(message = success("Delivered. Nice work.")) }
                    finished.trySend(Unit)
                }
                DeliveryCodeOutcome.WrongCode -> _state.update {
                    it.copy(code = "", codeError = "That code doesn't match. Ask the customer to read it again.")
                }
                DeliveryCodeOutcome.Locked -> _state.update {
                    it.copy(code = "", codeLocked = true, codeError = "Too many wrong codes. Contact Feast support to complete this delivery.")
                }
                DeliveryCodeOutcome.NotPickedUp -> _state.update {
                    it.copy(message = warning("The restaurant hasn't confirmed pickup yet. Ask them to enter your pickup code."))
                }
                DeliveryCodeOutcome.NotYourJob -> {
                    duty.setOnJob(false)
                    _state.update { it.copy(message = error("This job is no longer assigned to you.")) }
                    finished.trySend(Unit)
                }
                DeliveryCodeOutcome.NotActive -> _state.update {
                    it.copy(message = error("Your rider account isn't active. Go online again or contact support."))
                }
                is DeliveryCodeOutcome.Failed -> _state.update { it.copy(message = error(outcome.error.userMessage())) }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private suspend fun load() {
        when (val result = rider.currentAssignment()) {
            is FoodResult.Success -> {
                val assignment = result.value
                if (assignment == null) {
                    duty.setOnJob(false)
                    _state.update { it.copy(loading = false, assignment = null, actions = null) }
                } else {
                    apply(assignment)
                }
            }
            is FoodResult.Failure -> _state.update { it.copy(loading = false, message = it.message ?: result.error.asMessage()) }
        }
    }

    private suspend fun apply(assignment: DeliveryAssignmentDto) {
        val actions = JobActions.of(assignment)
        duty.setOnJob(actions.isActive)
        _state.update {
            it.copy(
                loading = false,
                assignment = assignment,
                actions = actions,
                restaurant = locations.restaurant(assignment),
                customer = locations.customer(assignment),
            )
        }
    }

    private companion object {
        const val POLL_MILLIS = 10_000L
        /** The fixtures' codes are four digits (order_get_200_out_for_delivery: "7390"). */
        const val CODE_LENGTH = 4
    }
}
