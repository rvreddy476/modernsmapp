package com.us.android.core.memberships.ui

import android.app.Activity
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.MembershipEvents
import com.us.android.core.memberships.data.MembershipLevel
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.membershipCode
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.memberships.payment.MembershipPaymentFlow
import com.us.android.core.memberships.payment.MembershipPaymentOpener
import com.us.android.core.memberships.payment.MembershipPaymentRequest
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentStateStore
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** The Join sheet: one creator's levels, the one picked, and where its payment stands. */
data class JoinUiState(
    val creatorId: String = "",
    val loading: Boolean = true,
    val loadError: String? = null,
    /** The creator offers no levels (or memberships are switched off). */
    val unavailable: String? = null,
    val levels: List<MembershipLevel> = emptyList(),
    val selectedId: String? = null,
    /** The level the post that opened the sheet needs, when one did. */
    val requiredLevelId: String? = null,
    /** That level's price as the post row carried it; used when the level is not among those offered. */
    val requiredPricePaise: Long? = null,
    val pay: MembershipPayState = MembershipPayState.Idle,
) {
    val selected: MembershipLevel? get() = levels.firstOrNull { it.id == selectedId }

    /** The price a level must reach to open the post that asked (access is by level price). */
    private val requiredPrice: Long?
        get() = levels.firstOrNull { it.id == requiredLevelId }?.pricePaise ?: requiredPricePaise

    /** Whether [level] opens the post that opened the sheet; true when no post asked. */
    fun opensThisPost(level: MembershipLevel): Boolean = requiredPrice?.let { level.pricePaise >= it } ?: true

    /** Join is offered: a level picked and nothing in flight. */
    val canJoin: Boolean
        get() = selected != null && (pay is MembershipPayState.Idle || pay is MembershipPayState.Refused)
}

/**
 * Joining a creator's membership, from any members-only surface: the levels
 * with their prices and perks, then `POST /subscribe/:creatorId` under ONE
 * idempotency key per level decision, Razorpay through `:core:payments`, and
 * the server's payment status until it says paid — only then is the post
 * unlocked ([MembershipEvents]). See [MembershipPaymentFlow] for the rules.
 *
 * Keyed per creator by the sheet (`hiltViewModel(key = "join:<creatorId>")`),
 * so its saved state is that creator's join alone.
 */
@HiltViewModel
class JoinMembershipViewModel @Inject constructor(
    private val repository: MembershipsRepository,
    private val opener: MembershipPaymentOpener,
    private val events: MembershipEvents,
    handoff: PaymentHandoff,
    payments: PaymentCoordinator,
    private val savedState: SavedStateHandle,
) : ViewModel() {

    private val store = object : PaymentStateStore {
        override fun get(key: String): String? = savedState[key]
        override fun set(key: String, value: String?) {
            savedState[key] = value
        }
    }

    private val flow = MembershipPaymentFlow(
        scope = viewModelScope,
        store = store,
        repository = repository,
        handoff = handoff,
        payments = payments,
        prefix = PREFIX,
    )

    private val _state = MutableStateFlow(JoinUiState())
    val state: StateFlow<JoinUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            flow.state.collect { pay ->
                _state.update { it.copy(pay = pay) }
                when {
                    pay is MembershipPayState.Paid -> events.accessChanged(_state.value.creatorId)
                    // Already a member: the lock was stale; say so and refresh it.
                    pay is MembershipPayState.Refused && pay.code == ALREADY_SUBSCRIBED ->
                        events.accessChanged(_state.value.creatorId)
                }
            }
        }
    }

    /** Opens the sheet for [creatorId]; [requiredLevelId] is the level the post asks for, if any. */
    fun bind(creatorId: String, requiredLevelId: String?, requiredPricePaise: Long? = null) {
        if (_state.value.creatorId == creatorId && !_state.value.loading) return
        _state.update {
            it.copy(creatorId = creatorId, requiredLevelId = requiredLevelId, requiredPricePaise = requiredPricePaise)
        }
        savedState[KEY_CREATOR] = creatorId
        viewModelScope.launch { load() }
    }

    fun retryLoad() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch { load() }
    }

    fun select(levelId: String) {
        if (_state.value.pay !is MembershipPayState.Idle && _state.value.pay !is MembershipPayState.Refused) return
        _state.update { it.copy(selectedId = levelId) }
    }

    fun join() {
        val current = _state.value
        val level = current.selected ?: return
        if (!current.canJoin) return
        savedState[KEY_LEVEL] = level.id
        savedState[KEY_LEVEL_NAME] = level.name
        flow.pay(decision = "join:${current.creatorId}:${level.id}", label = label(level.name)) { key ->
            repository.join(current.creatorId, level.id, key)
        }
    }

    /** Pay again after a failure — the SAME key, which reopens the payment — or check again after a timeout. */
    fun retryPayment() = flow.retry()

    /** Hands the sheet request to the Activity, once. */
    fun openSheet(activity: Activity, request: MembershipPaymentRequest) {
        opener.start(activity, request)
        flow.onSheetOpened(request)
    }

    /** The sheet is going away mid-payment: release the launcher's slot. */
    fun abandon() {
        val opening = _state.value.pay as? MembershipPayState.OpeningPayment ?: return
        if (opening.request.attempt == flow.activeAttempt) opener.abandon(opening.request)
    }

    /** Leaves an outcome: the next Join is a new decision. */
    fun done() = flow.reset()

    private suspend fun load() {
        val creatorId = _state.value.creatorId
        when (val result = repository.levelsOf(creatorId)) {
            is AppResult.Failure -> {
                val code = result.error.membershipCode()
                _state.update {
                    if (code == NOT_ENABLED) {
                        it.copy(loading = false, unavailable = membershipRefusal(result.error))
                    } else {
                        it.copy(loading = false, loadError = membershipRefusal(result.error, "Levels didn't load."))
                    }
                }
            }
            is AppResult.Success -> {
                val levels = result.data
                _state.update { s ->
                    s.copy(
                        loading = false,
                        loadError = null,
                        levels = levels,
                        unavailable = NO_LEVELS.takeIf { levels.isEmpty() },
                        selectedId = defaultSelection(s, levels),
                    )
                }
                resumeIfNeeded()
            }
        }
    }

    /** The level a resumed join was for; else the post's level; else the cheapest that opens it. */
    private fun defaultSelection(s: JoinUiState, levels: List<MembershipLevel>): String? {
        val saved = savedState.get<String>(KEY_LEVEL)?.takeIf { id -> levels.any { it.id == id } }
        if (saved != null) return saved
        s.selectedId?.takeIf { id -> levels.any { it.id == id } }?.let { return it }
        val probe = s.copy(levels = levels)
        return levels.firstOrNull { probe.opensThisPost(it) }?.id ?: levels.firstOrNull()?.id
    }

    private fun resumeIfNeeded() {
        if (!flow.hasSomethingToResume) return
        val creatorId = _state.value.creatorId
        val levelId = savedState.get<String>(KEY_LEVEL) ?: return
        val name = savedState.get<String>(KEY_LEVEL_NAME).orEmpty()
        flow.resume(open = { key -> repository.join(creatorId, levelId, key) }, label = label(name))
    }

    private fun label(levelName: String) = if (levelName.isBlank()) "Membership" else "$levelName membership"

    companion object {
        private const val PREFIX = "memberships.join"
        private const val KEY_CREATOR = "memberships.join.creatorId"
        private const val KEY_LEVEL = "memberships.join.levelId"
        private const val KEY_LEVEL_NAME = "memberships.join.levelName"
        const val ALREADY_SUBSCRIBED = "ALREADY_SUBSCRIBED"
        private const val NOT_ENABLED = "MEMBERSHIPS_NOT_ENABLED"
        private const val NO_LEVELS = "This creator doesn't offer memberships right now."
    }
}
