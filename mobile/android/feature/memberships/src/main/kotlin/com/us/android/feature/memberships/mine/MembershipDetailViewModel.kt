package com.us.android.feature.memberships.mine

import android.app.Activity
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.navigation.toRoute
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.getOrNull
import com.us.android.core.common.result.map
import com.us.android.core.memberships.MembershipEvents
import com.us.android.core.memberships.data.LevelChange
import com.us.android.core.memberships.data.MembershipCheckout
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.MyMembership
import com.us.android.core.memberships.data.PendingDowngrade
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.memberships.data.rupees
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.memberships.payment.MembershipPaymentFlow
import com.us.android.core.memberships.payment.MembershipPaymentOpener
import com.us.android.core.memberships.payment.MembershipPaymentRequest
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.profile.data.ProfileRepository
import com.us.android.feature.memberships.navigation.MembershipDetailRoute
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class MembershipDetailUiState(
    val loading: Boolean = true,
    val loadError: String? = null,
    /** The membership is not in the viewer's list (ended long ago, or not theirs). */
    val missing: Boolean = false,
    val membership: MyMembership? = null,
    val creatorName: String = "",
    /** The creator's active levels, for Change level. */
    val levels: List<MembershipLevel> = emptyList(),
    val pay: MembershipPayState = MembershipPayState.Idle,
    val cancelling: Boolean = false,
    val message: String? = null,
) {
    /** The levels the membership could move to: active, and not the one it is on. */
    val otherLevels: List<MembershipLevel>
        get() = levels.filter { it.id != membership?.levelId }

    val busy: Boolean
        get() = cancelling || pay is MembershipPayState.Submitting || pay is MembershipPayState.OpeningPayment ||
            pay is MembershipPayState.InSheet || pay is MembershipPayState.Confirming
}

/**
 * One membership (paid memberships, 2026-10-23), opened from the list or from
 * a member notice (`/memberships/{id}`): its dates, Renew (pay the next
 * month — renewal is by reminder, nothing is charged automatically), Change
 * level (a paid upgrade, or a downgrade from next month) and Cancel (access
 * stays until the month ends). Renew and a paid upgrade pay through
 * [MembershipPaymentFlow], exactly like a join.
 */
// Constructor injection of the screen's collaborators; a wrapper would add indirection, not clarity.
@Suppress("LongParameterList")
@HiltViewModel
class MembershipDetailViewModel @Inject constructor(
    private val repository: MembershipsRepository,
    private val profiles: ProfileRepository,
    private val opener: MembershipPaymentOpener,
    private val events: MembershipEvents,
    handoff: PaymentHandoff,
    payments: PaymentCoordinator,
    private val savedState: SavedStateHandle,
) : ViewModel() {

    val subscriptionId: String = savedState.toRoute<MembershipDetailRoute>().subscriptionId

    private val flow = MembershipPaymentFlow(
        scope = viewModelScope,
        store = object : PaymentStateStore {
            override fun get(key: String): String? = savedState[key]
            override fun set(key: String, value: String?) {
                savedState[key] = value
            }
        },
        repository = repository,
        handoff = handoff,
        payments = payments,
        prefix = PREFIX,
    )

    private val _state = MutableStateFlow(MembershipDetailUiState())
    val state: StateFlow<MembershipDetailUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            flow.state.collect { pay ->
                _state.update { it.copy(pay = pay) }
                if (pay is MembershipPayState.Paid || pay is MembershipPayState.Applied) {
                    _state.value.membership?.creatorId?.let(events::accessChanged)
                    load()
                }
            }
        }
        viewModelScope.launch {
            load()
            resumeIfNeeded()
        }
    }

    fun refresh() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch { load() }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Pay for the next month. */
    fun renew() {
        if (_state.value.busy) return
        savedState[KEY_ACTION] = ACTION_RENEW
        flow.pay(decision = "renew:$subscriptionId", label = label()) { _ -> renewCall() }
    }

    /** Move to [levelId]: up is paid now (the pro-rata difference), down starts next month. */
    fun changeLevel(levelId: String) {
        if (_state.value.busy) return
        savedState[KEY_ACTION] = ACTION_CHANGE
        savedState[KEY_TARGET] = levelId
        flow.pay(decision = "change:$subscriptionId:$levelId", label = label()) { _ -> changeCall(levelId) }
    }

    /** Cancel at the end of the paid month. Access stays until then; no refund (founder, 23 Oct). */
    fun cancel() {
        if (_state.value.busy) return
        _state.update { it.copy(cancelling = true) }
        viewModelScope.launch {
            when (val result = repository.cancel(subscriptionId)) {
                is AppResult.Success -> {
                    _state.update { it.copy(cancelling = false, message = CANCELLED) }
                    load()
                }
                is AppResult.Failure -> _state.update {
                    it.copy(cancelling = false, message = membershipRefusal(result.error))
                }
            }
        }
    }

    fun retryPayment() = flow.retry()

    fun donePayment() = flow.reset()

    fun openSheet(activity: Activity, request: MembershipPaymentRequest) {
        opener.start(activity, request)
        flow.onSheetOpened(request)
    }

    fun abandon() {
        val opening = _state.value.pay as? MembershipPayState.OpeningPayment ?: return
        if (opening.request.attempt == flow.activeAttempt) opener.abandon(opening.request)
    }

    private suspend fun renewCall(): AppResult<MembershipCheckout?> = repository.renew(subscriptionId)

    private suspend fun changeCall(levelId: String): AppResult<MembershipCheckout?> =
        repository.changeLevel(subscriptionId, levelId).map { change ->
            when (change) {
                is LevelChange.Pay -> change.checkout
                LevelChange.Upgraded, LevelChange.Unchanged, is LevelChange.Scheduled -> {
                    _state.update { it.copy(message = change.sentence()) }
                    null
                }
            }
        }

    private fun resumeIfNeeded() {
        if (!flow.hasSomethingToResume) return
        val open: (suspend (String) -> AppResult<MembershipCheckout?>)? = when (savedState.get<String>(KEY_ACTION)) {
            ACTION_RENEW -> { _ -> renewCall() }
            ACTION_CHANGE -> savedState.get<String>(KEY_TARGET)?.let { target -> { _ -> changeCall(target) } }
            else -> null
        }
        flow.resume(open, label())
    }

    /** `GET /v1/monetization/memberships/:id` (perks, scheduled downgrade); 404 is "not found", not an error. */
    private suspend fun load() {
        when (val result = repository.myMembership(subscriptionId)) {
            is AppResult.Failure -> _state.update {
                if (result.error is AppError.NotFound) {
                    it.copy(loading = false, loadError = null, missing = true, membership = null)
                } else {
                    it.copy(loading = false, loadError = membershipRefusal(result.error, "This membership didn't load."))
                }
            }
            is AppResult.Success -> {
                val membership = result.data
                // The server names the creator; a profile read only when its own lookup failed.
                val name = membership.creatorName.ifBlank { _state.value.creatorName }.ifBlank {
                    profiles.getProfile(membership.creatorId).getOrNull()
                        ?.let { it.displayName.ifBlank { it.username } }.orEmpty()
                }
                val levels = repository.levelsOf(membership.creatorId).getOrNull() ?: _state.value.levels
                _state.update {
                    it.copy(
                        loading = false,
                        loadError = null,
                        missing = false,
                        membership = membership,
                        creatorName = name,
                        levels = levels
                    )
                }
            }
        }
    }

    private fun label(): String {
        val m = _state.value.membership ?: return "Membership"
        return if (m.levelName.isBlank()) "Membership" else "${m.levelName} membership"
    }

    private companion object {
        const val PREFIX = "memberships.detail"
        const val KEY_ACTION = "memberships.detail.action"
        const val KEY_TARGET = "memberships.detail.target"
        const val ACTION_RENEW = "renew"
        const val ACTION_CHANGE = "change"
        const val CANCELLED = "Cancelled. You keep access until the month ends."
    }
}

/** What a level change with nothing to pay did, in a sentence. */
internal fun LevelChange.sentence(): String = when (this) {
    is LevelChange.Pay -> "Pay the difference to move up now."
    LevelChange.Upgraded -> "You're on the new level now."
    LevelChange.Unchanged -> "You're already on that level."
    is LevelChange.Scheduled -> {
        val date = membershipDate(effectiveAt)
        if (date.isBlank()) {
            "Your new level starts next month."
        } else {
            "Your new level starts on $date. Nothing is charged now."
        }
    }
}

/** "Moves to Fan (₹49 a month) on 23 Nov 2026": a downgrade waiting for the next month. */
internal fun pendingDowngradeLine(down: PendingDowngrade): String {
    val date = membershipDate(down.effectiveAt).ifBlank { "next month" }
    return "Moves to ${down.levelName.ifBlank { "a lower level" }} (${rupees(down.pricePaise)} a month) on $date"
}
