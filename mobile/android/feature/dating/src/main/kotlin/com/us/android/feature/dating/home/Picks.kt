package com.us.android.feature.dating.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.DeviceZone
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.ActionSource
import com.us.android.feature.dating.network.PicksDto
import com.us.android.feature.dating.network.PulseCardDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.ui.infoMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import javax.inject.Inject

/** Today's picks (mechanic M7), in display terms. */
sealed interface PicksState {
    data object Loading : PicksState

    /** The server's flag is off (`MECHANIC_NOT_ENABLED`): there is no Picks tab at all. */
    data object Hidden : PicksState

    data class Failed(val message: String) : PicksState

    data class Loaded(
        /** In the server's order, which holds all day; empty draws the empty pane. */
        val cards: List<CardUi>,
        /** The next local midnight, when a new set arrives; null when the server sent none we can read. */
        val resetsAt: Instant?,
    ) : PicksState
}

/** The picks' words. Our own. */
object PicksCopy {
    const val TAB = "Picks"
    const val TITLE = "Today's picks"
    const val BODY = "A few people chosen for you today. Sparking or passing here doesn't use your Pulse cards."
    const val EMPTY_TITLE = "No picks today"
    const val LOAD_FAILED = "Picks didn't load"
    const val SPARK_SENT = "Spark sent."

    /** "New picks at 12:00 AM tomorrow"; a time behind us, or none, says when in general terms. */
    fun resetLine(resetsAt: Instant?, now: Instant, zone: ZoneId): String =
        resetsAt?.let { DeckCopy.whenLabel(it, now, zone) }?.let { "New picks at $it" } ?: "New picks every day at midnight"

    fun emptyBody(resetsAt: Instant?, now: Instant, zone: ZoneId): String =
        resetsAt?.let { DeckCopy.whenLabel(it, now, zone) }?.let { "A fresh set arrives at $it." } ?: "A fresh set arrives after midnight."
}

/**
 * The Picks tab (mechanic M7): up to ten people chosen for the viewer's local
 * day, apart from the deck, refreshed at local midnight.
 *
 * ## Time zone
 *
 * The device's IANA zone goes with the read. A zone the server does not know
 * (`400 INVALID_TIMEZONE` — some devices report a bare offset) is retried ONCE
 * without one, and the server then uses its default day.
 *
 * ## Acting on a pick
 *
 * Spark and pass carry `source: "picks"`, so they spend no deck card. The pick
 * leaves the list once the server has taken the action; a refusal leaves it
 * where it was, except `CANDIDATE_UNAVAILABLE`, when there is no one to keep.
 *
 * `MECHANIC_NOT_ENABLED`, on the read or on an action, hides Picks for the
 * session. A trip started or ended (mechanic M8) reads the picks again: while
 * travelling they are the destination's.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class PicksViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val safety: SafetyActions,
    private val urls: DatingPhotoUrls,
    private val zone: DeviceZone,
) : ViewModel() {

    private val rows = MutableStateFlow<List<PulseCardDto>?>(null)
    private val resetsAt = MutableStateFlow<Instant?>(null)
    private val failure = MutableStateFlow<String?>(null)
    private val hidden = MutableStateFlow(session.isMechanicDisabled(MECHANIC))

    val state: StateFlow<PicksState> =
        combine(rows, resetsAt, failure, hidden, session.removed) { list, resets, fail, off, removed ->
            when {
                off -> PicksState.Hidden
                list == null && fail != null -> PicksState.Failed(fail)
                list == null -> PicksState.Loading
                // Filtered on every emission, as the deck is: a stale read can
                // never put a blocked person back.
                else -> PicksState.Loaded(list.filterNot { it.profile.userId in removed }.map { it.toCardUi(urls) }, resets)
            }
        }.stateIn(viewModelScope, SharingStarted.Eagerly, PicksState.Loading)

    /** The tab is drawn once the server has said picks exist (or failed to say, so a retry is offered). */
    val visible: StateFlow<Boolean> =
        state.map { it is PicksState.Loaded || it is PicksState.Failed }.stateIn(viewModelScope, SharingStarted.Eagerly, false)

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val _busy = MutableStateFlow<String?>(null)
    val busy: StateFlow<String?> = _busy.asStateFlow()

    private val celebrations = MatchCelebrations(repository, urls, viewModelScope)
    val celebration: StateFlow<MatchCelebration?> = celebrations.state
    val hello: StateFlow<HelloTarget?> = celebrations.hello

    init {
        refresh()
        viewModelScope.launch { session.travelVersion.drop(1).collect { refresh() } }
    }

    fun refresh() {
        if (hidden.value) return
        viewModelScope.launch { load() }
    }

    /** Shown again: past the reset time, the day has turned and a new set is waiting. */
    fun refreshIfStale(now: Instant = Instant.now()) {
        val resets = resetsAt.value ?: return
        if (!now.isBefore(resets)) refresh()
    }

    private suspend fun load() {
        when (val result = fetch()) {
            is DatingResult.Success -> apply(result.value)
            is DatingResult.Failure -> if (result.error.code == CODE_MECHANIC_NOT_ENABLED) {
                switchOff()
            } else {
                failure.value = DatingCopy.forError(result.error, repository.json)
            }
        }
    }

    /** The device zone first; one retry without it when the server does not know the zone. */
    private suspend fun fetch(): DatingResult<PicksDto> {
        val tz = runCatching { zone.id() }.getOrNull()?.trim()?.takeIf { it.isNotEmpty() }
            ?: return repository.picks(null)
        val first = repository.picks(tz)
        return if (first is DatingResult.Failure && first.error.code == CODE_INVALID_TIMEZONE) repository.picks(null) else first
    }

    private fun apply(dto: PicksDto) {
        failure.value = null
        // Each person once, a card that names no one dropped, and never more
        // than the server's daily ceiling.
        rows.value = dto.data
            .filter { it.profile.userId.isNotBlank() }
            .distinctBy { it.profile.userId }
            .take(MAX_PICKS)
        resetsAt.value = parseInstant(dto.meta?.resetsAt)
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun dismissCelebration() = celebrations.dismiss()

    fun sayHello() = celebrations.sayHello()

    fun helloHandled() = celebrations.helloHandled()

    /** A spark from the picks: `source: "picks"`, so no deck card is spent. */
    fun spark(userId: String): Boolean = act(userId) {
        when (val result = repository.spark(userId, source = ActionSource.PICKS)) {
            is DatingResult.Success -> {
                val card = rows.value?.firstOrNull { it.profile.userId == userId }?.profile
                drop(userId)
                val created = result.value
                if (created.matched && created.matchId != null) {
                    celebrations.show(created.matchId, card?.firstName.orEmpty(), urls.forViewer(card?.primaryPhotoUrl, matched = false))
                } else {
                    _message.value = successMessage(PicksCopy.SPARK_SENT)
                }
            }
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    /** A pass from the picks: `source: "picks"`. Not undoable — undo belongs to the deck. */
    fun pass(userId: String): Boolean = act(userId) {
        when (val result = repository.pass(userId, source = ActionSource.PICKS)) {
            is DatingResult.Success -> drop(userId)
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    fun block(userId: String): Boolean = act(userId) {
        when (val result = safety.block(userId)) {
            is DatingResult.Success -> _message.value = successMessage("Blocked. You won't see each other again.")
            is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
        }
    }

    fun report(draft: ReportDraft): Boolean = act(draft.targetId) {
        when (val result = safety.report(draft)) {
            is DatingResult.Success -> _message.value = successMessage("Thanks for telling us. We've blocked them for you.")
            is DatingResult.Failure -> _message.value = reportFailure(result.error)
        }
    }

    private fun refused(userId: String, error: DatingError) {
        when (error.code) {
            CODE_CANDIDATE_UNAVAILABLE -> {
                drop(userId)
                _message.value = DatingCopy.message(error)
            }
            CODE_MECHANIC_NOT_ENABLED -> {
                switchOff()
                _message.value = infoMessage("Picks aren't available right now.")
            }
            else -> _message.value = DatingCopy.message(error, repository.json)
        }
    }

    private fun drop(userId: String) = rows.update { list -> list?.filterNot { it.profile.userId == userId } }

    private fun switchOff() {
        session.disableMechanic(MECHANIC)
        hidden.value = true
    }

    /** One action at a time. False when another is in flight, and nothing was started. */
    private fun act(userId: String, block: suspend () -> Unit): Boolean {
        if (_busy.value != null || userId.isBlank()) return false
        _busy.value = userId
        viewModelScope.launch {
            try {
                block()
            } finally {
                _busy.value = null
            }
        }
        return true
    }

    companion object {
        /** The server's ceiling (`service.MaxDailyPicks`). */
        const val MAX_PICKS = 10
        const val MECHANIC = "picks"
        private const val CODE_INVALID_TIMEZONE = "INVALID_TIMEZONE"
        private const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        private const val CODE_CANDIDATE_UNAVAILABLE = "CANDIDATE_UNAVAILABLE"
    }
}
