package com.us.android.feature.rider.job

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.DeliveryBatchDto
import com.us.android.core.food.network.BatchRoutePreviewDto
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
import kotlinx.coroutines.Job
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
    /** Restaurant, customer (only while the assignment carries `drop`), pay, ETA and maps links. */
    val details: JobDetails? = null,
    val busy: Boolean = false,
    val code: String = "",
    val codeError: String? = null,
    val verifying: Boolean = false,
    /** Five wrong codes: the field is closed and support must help. */
    val codeLocked: Boolean = false,
    val confirmingRelease: Boolean = false,
    val message: UsMessage? = null,
    val batch: DeliveryBatchDto? = null,
    val batchJobs: List<DeliveryAssignmentDto> = emptyList(),
    val routePreview: BatchRoutePreviewDto? = null,
    val routeLoading: Boolean = false,
    val routeError: String? = null,
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
) : ViewModel() {

    private val _state = MutableStateFlow(ActiveJobUiState())
    val state: StateFlow<ActiveJobUiState> = _state.asStateFlow()

    private val finished = Channel<Unit>(Channel.CONFLATED)
    val finishedEvents: Flow<Unit> = finished.receiveAsFlow()

    private val idempotencyKeys = HashMap<Pair<String, RiderAssignmentStep>, String>()
    private var routeJob: Job? = null
    private var routeExpiry: Job? = null
    private var routeRevision = 0L

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
        resetRoute()
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
                        loadBatch(result.value)
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

    /** Explicit per-order confirmation; suggestRoute never calls this. */
    fun confirmBatchOrder(orderId: String) {
        val snapshot = _state.value
        val assignment = snapshot.assignment ?: return
        val batch = snapshot.batch ?: return
        if (snapshot.busy || !BatchRouteRules.visible(assignment, batch)) return
        val member = BatchRouteRules.members(assignment, batch, snapshot.batchJobs)
            .singleOrNull { it.orderId == orderId && it.status == "ASSIGNED" } ?: return
        resetRoute()
        _state.update { it.copy(busy = true) }
        viewModelScope.launch {
            val step = RiderAssignmentStep.ACCEPT
            val key = idempotencyKeys.getOrPut(member.id to step) { UUID.randomUUID().toString() }
            val result = rider.step(member.id, step, key)
            if (!(result is FoodResult.Failure && result.error is FoodError.Network)) idempotencyKeys.remove(member.id to step)
            if (result is FoodResult.Failure) _state.update { it.copy(message = result.error.asMessage()) }
            load()
            _state.update { it.copy(busy = false) }
        }
    }

    fun suggestRoute() {
        val snapshot = _state.value
        val assignment = snapshot.assignment ?: return
        val batch = snapshot.batch ?: return
        if (snapshot.routeLoading || snapshot.busy || !BatchRouteRules.ready(assignment, batch, snapshot.batchJobs)) return
        resetRoute()
        val revision = routeRevision
        _state.update { it.copy(routeLoading = true) }
        routeJob = viewModelScope.launch {
            val result = rider.previewBatchRoute(assignment.orderId)
            if (revision != routeRevision) return@launch
            // Re-read access/membership before presenting even a successful response.
            val fresh = rider.batch(assignment.orderId)
            if (revision != routeRevision) return@launch
            if (fresh !is FoodResult.Success || fresh.value != batch) {
                resetRoute()
                _state.update {
                    it.copy(
                        batch = null,
                        batchJobs = emptyList(),
                        message = warning("Your batch changed or couldn't be checked. Try again after it refreshes."),
                    )
                }
                return@launch
            }
            when (result) {
                is FoodResult.Success -> {
                    if (!BatchRouteRules.valid(result.value, batch)) {
                        _state.update { it.copy(routeLoading = false, routeError = "We couldn't verify this suggestion. Follow your saved stop order.") }
                        return@launch
                    }
                    _state.update { it.copy(routeLoading = false, routePreview = result.value) }
                    routeExpiry = viewModelScope.launch {
                        delay(ROUTE_PREVIEW_MILLIS)
                        if (revision == routeRevision) {
                            _state.update { it.copy(routePreview = null, routeError = "This suggestion has expired. Request a fresh one before pickup.") }
                        }
                    }
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(routeLoading = false, routeError = BatchRouteRules.failure(result.error))
                }
            }
        }
    }

    private fun resetRoute() {
        routeRevision++
        routeJob?.cancel()
        routeExpiry?.cancel()
        _state.update { it.copy(routePreview = null, routeLoading = false, routeError = null) }
    }

    private suspend fun loadBatch(assignment: DeliveryAssignmentDto) {
        if (!BatchRouteRules.batchWindow(assignment)) return
        val result = rider.batch(assignment.orderId)
        val batch = (result as? FoodResult.Success)?.value?.takeIf { BatchRouteRules.visible(assignment, it) }
        val members = if (batch != null) {
            val jobs = (rider.assignments() as? FoodResult.Success)?.value.orEmpty()
            BatchRouteRules.members(assignment, batch, jobs)
        } else emptyList()
        val current = _state.value.assignment
        if (!sameRouteContext(current, assignment)) return
        if (batch != _state.value.batch || members != _state.value.batchJobs) resetRoute()
        _state.update { it.copy(batch = batch, batchJobs = members) }
    }

    private suspend fun load() {
        when (val result = rider.currentAssignment()) {
            is FoodResult.Success -> {
                val assignment = result.value
                if (assignment == null) {
                    resetRoute()
                    duty.setOnJob(false)
                    _state.update { it.copy(loading = false, assignment = null, actions = null, details = null, batch = null, batchJobs = emptyList()) }
                } else {
                    apply(assignment)
                    loadBatch(assignment)
                }
            }
            is FoodResult.Failure -> {
                resetRoute()
                _state.update { it.copy(loading = false, batch = null, batchJobs = emptyList(), message = it.message ?: result.error.asMessage()) }
            }
        }
    }

    private fun apply(assignment: DeliveryAssignmentDto) {
        val keepBatch = sameRouteContext(_state.value.assignment, assignment) && BatchRouteRules.batchWindow(assignment)
        if (!keepBatch) resetRoute()
        val actions = JobActions.of(assignment)
        duty.setOnJob(actions.isActive)
        _state.update {
            it.copy(
                loading = false,
                assignment = assignment,
                actions = actions,
                details = JobDetails.of(assignment),
                batch = it.batch.takeIf { keepBatch },
                batchJobs = if (keepBatch) it.batchJobs else emptyList(),
            )
        }
    }

    private companion object {
        const val ROUTE_PREVIEW_MILLIS = 5 * 60_000L
        const val POLL_MILLIS = 10_000L
        /** The fixtures' codes are four digits (order_get_200_out_for_delivery: "7390"). */
        const val CODE_LENGTH = 4
    }
}

private fun sameRouteContext(a: DeliveryAssignmentDto?, b: DeliveryAssignmentDto): Boolean =
    a?.id == b.id && a.orderId == b.orderId && a.status == b.status && a.orderStatus == b.orderStatus &&
        a.deliveryPartnerId == b.deliveryPartnerId && a.restaurantId == b.restaurantId &&
        a.restaurant == b.restaurant && a.drop == b.drop
