package com.us.android.feature.dating.home

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingIntent
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.DistanceBucket
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.data.valueOrNull
import com.us.android.feature.dating.network.AllowancesDto
import com.us.android.feature.dating.network.DatingPersonDto
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.PulseCardDto
import com.us.android.feature.dating.network.RateLimitDetailsDto
import com.us.android.feature.dating.network.RewindDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.travel.TravelRules
import com.us.android.feature.dating.travel.visitingLabel
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.infoMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A person on a Pulse card, in display terms only: a distance BUCKET label, a BLURRED photo. */
data class CardUi(
    val userId: String,
    val name: String,
    val age: Int,
    val city: String,
    /** A bucket label ("5–10 km") or null. Never a number. */
    val distance: String?,
    val lastActive: String?,
    val verified: Boolean,
    val photoUrl: String?,
    val photoId: String?,
    val reasons: List<String>,
    /** The pre-match block: the gallery, the bio and the prompt answers you decide on. */
    val detail: PersonDetailUi? = null,
    /** Mechanic M8: "Visiting Hyderabad" while they are on a trip, else null. */
    val visiting: String? = null,
)

/** A deck-shaped card (the deck and the picks share it) in display terms. */
internal fun PulseCardDto.toCardUi(urls: DatingPhotoUrls): CardUi = CardUi(
    userId = profile.userId,
    name = profile.firstName,
    age = profile.age,
    city = profile.city,
    distance = DistanceBucket.labelFor(profile.distanceBucket),
    lastActive = profile.lastActiveLabel?.takeIf { it.isNotBlank() },
    verified = profile.trustTier == "selfie" || profile.trustTier == "aadhaar",
    photoUrl = urls.forViewer(profile.primaryPhotoUrl, matched = false),
    photoId = PhotoRules.photoIdOf(profile.primaryPhotoUrl),
    reasons = matchReasons.map { it.summary }.filter { it.isNotBlank() },
    detail = profile.detail.toUi(urls),
    visiting = visitingLabel(profile.travelling, profile.city),
)

sealed interface ListState<out T> {
    data object Loading : ListState<Nothing>

    data class Items<T>(val items: List<T>, val gated: Boolean = false) : ListState<T>

    data class Failed(val message: String) : ListState<Nothing>
}

/**
 * A mutual spark just became a match.
 *
 * It opens with what the screen that made it already had — the name and the
 * photo on the card — and is completed from `GET /matches/:id`: the server's
 * own card for the other person (the variant a MATCH may see) and the
 * conversation, once the server has created it.
 */
data class MatchCelebration(
    val matchId: String,
    val name: String,
    val photoUrl: String? = null,
    /** Null until the server has created the chat. */
    val conversationId: String? = null,
    /** "Say hello" was tapped and the chat is being looked up. */
    val opening: Boolean = false,
    /**
     * Mechanic M5: the other person writes first, so chat would refuse a
     * hello. "Say hello" leads to the match screen instead, where the opening
     * questions are.
     */
    val waitingForThem: Boolean = false,
)

/** Where "Say hello" leads: the chat when it exists, the match's own screen while it does not. */
sealed interface HelloTarget {
    data class Chat(val conversationId: String, val title: String) : HelloTarget

    data class Match(val matchId: String) : HelloTarget
}

/** The match screen's state, shared by the deck and the incoming sparks — either can make a match. */
internal class MatchCelebrations(
    private val repository: DatingRepository,
    private val urls: DatingPhotoUrls,
    private val scope: CoroutineScope,
) {
    private val _state = MutableStateFlow<MatchCelebration?>(null)
    val state: StateFlow<MatchCelebration?> = _state.asStateFlow()

    private val _hello = MutableStateFlow<HelloTarget?>(null)
    val hello: StateFlow<HelloTarget?> = _hello.asStateFlow()

    fun show(matchId: String, name: String, photoUrl: String?) {
        _state.value = MatchCelebration(matchId, name, photoUrl)
        scope.launch { load(matchId) }
    }

    fun dismiss() {
        _state.value = null
    }

    fun helloHandled() {
        _hello.value = null
    }

    /** Opens the chat if the match has one — asking once more if it did not yet — else the match itself. */
    fun sayHello() {
        val current = _state.value ?: return
        if (current.opening) return
        _state.value = current.copy(opening = true)
        scope.launch {
            val conversation = current.conversationId ?: load(current.matchId)
            val latest = _state.value?.takeIf { it.matchId == current.matchId } ?: current
            _hello.value = if (conversation != null && !latest.waitingForThem) {
                HelloTarget.Chat(conversation, latest.name.ifBlank { "Match" })
            } else {
                HelloTarget.Match(current.matchId)
            }
            _state.value = null
        }
    }

    /** Fills the celebration from the match and returns its conversation id, if any. A failure changes nothing. */
    private suspend fun load(matchId: String): String? {
        val dto = repository.match(matchId).valueOrNull() ?: return null
        val conversation = dto.conversationId?.takeIf { it.isNotBlank() }
        _state.update { current ->
            if (current?.matchId != matchId) {
                current
            } else {
                current.copy(
                    name = dto.person?.firstName?.takeIf { it.isNotBlank() } ?: current.name,
                    photoUrl = urls.forPerson(dto.person) ?: current.photoUrl,
                    conversationId = conversation ?: current.conversationId,
                    waitingForThem = dto.firstMove?.youMoveFirst == false,
                )
            }
        }
        return conversation
    }
}

/** Shared by the three lists: the server's rows, a load failure, and the person-removal filter. */
private class RemovableList<Dto>(private val idOf: (Dto) -> String) {
    val rows = MutableStateFlow<List<Dto>?>(null)
    val failure = MutableStateFlow<String?>(null)
    val gated = MutableStateFlow(false)

    fun drop(personId: String) = rows.update { list -> list?.filterNot { idOf(it) == personId } }

    fun <Ui> state(session: DatingSession, toUi: (Dto) -> Ui) =
        combine(rows, failure, gated, session.removed) { list, fail, isGated, removed ->
            when {
                list == null && fail != null -> ListState.Failed(fail)
                list == null -> ListState.Loading
                // Filtered on EVERY emission: a stale reload that still carries a
                // blocked person can never put them back on screen.
                else -> ListState.Items(list.filterNot { idOf(it) in removed }.map(toUi), isGated)
            }
        }
}

/**
 * Pulse today: a deck of cards with spark, pass and stash, plus report and
 * block. A card leaves the deck once the server has taken the action.
 *
 * ## One action at a time
 *
 * An action is refused while another is in flight ([busy]); each one says so
 * by returning false, and the screen puts the card back where it was. While it
 * runs, [deck]'s `leaving` names the card and the way out, so the screen can
 * send it off; a refusal clears it and the card comes back, except for
 * `CANDIDATE_UNAVAILABLE`, where there is no one to come back to.
 *
 * ## The refilling deck (mechanic M1)
 *
 * With the server's refill flag on, `GET /pulse/today` serves a batch and its
 * meta carries the daily allowance. When the stack on screen runs empty and
 * the LAST meta said cards remain, the next batch is fetched. Without a
 * `daily_limit` in that meta — the flag is off — nothing is refetched, exactly
 * as before; with none remaining, the screen shows the out-of-cards pane.
 *
 * ## Allowances, undo and Super Spark (mechanics M10, M2, M3)
 *
 * `GET /allowances` is read with the deck and again after every action that
 * can change it. A mechanic ABSENT from it is off, and its control is not
 * drawn: [DeckUi.rewind] and [DeckUi.superSpark] are null. A route that
 * answers `MECHANIC_NOT_ENABLED` switches its mechanic off for the session.
 *
 * Undo is offered only while the last deck action in this session was a pass
 * the server took ([DeckUi.rewindable]); it puts the server's card back on top,
 * or refetches the deck when the server sent none. Each allowance refusal has
 * its own pane, with the reset time the server sent.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class PulseViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val safety: SafetyActions,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val list = RemovableList<PulseCardDto> { it.profile.userId }

    val state: StateFlow<ListState<CardUi>> =
        list.state(session) { it.toUi() }.stateIn(viewModelScope, SharingStarted.Eagerly, ListState.Loading)

    private val _deck = MutableStateFlow(DeckUi())
    val deck: StateFlow<DeckUi> = _deck.asStateFlow()

    /** What the server's last meta said was left. The refetch rule reads THIS, not the local countdown. */
    private var remainingAtLastMeta = 0

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val celebrations = MatchCelebrations(repository, urls, viewModelScope)
    val celebration: StateFlow<MatchCelebration?> = celebrations.state
    val hello: StateFlow<HelloTarget?> = celebrations.hello

    private val _busy = MutableStateFlow<String?>(null)
    val busy: StateFlow<String?> = _busy.asStateFlow()

    init {
        refresh()
        // Mechanic M6: saved filters change who the server deals, so the deck
        // on screen is replaced by a fresh batch. The current value is skipped:
        // this view model has just loaded.
        viewModelScope.launch { session.filtersVersion.drop(1).collect { refresh() } }
        // Mechanic M8: a trip started or ended — the deck is now another city's.
        viewModelScope.launch { session.travelVersion.drop(1).collect { refresh() } }
    }

    fun refresh() {
        viewModelScope.launch {
            load()
            loadAllowances()
            loadTravel()
        }
    }

    /** Reads every allowance again: after an action, or when the deck is shown again (a purchase may have landed). */
    fun refreshAllowances() {
        viewModelScope.launch { loadAllowances() }
    }

    /** A failed read changes nothing: the last answer stands, and the next action refreshes it. */
    private suspend fun loadAllowances() {
        val allowances = repository.allowances().valueOrNull() ?: return
        _deck.update { it.withAllowances(allowances) }
    }

    private fun DeckUi.withAllowances(allowances: AllowancesDto): DeckUi {
        val rewind = allowances.rewind?.takeUnless { session.isMechanicDisabled(MECHANIC_REWIND) }
        val superSpark = allowances.superSpark?.takeUnless { session.isMechanicDisabled(MECHANIC_SUPER_SPARK) }
        return copy(
            rewind = rewind?.toUi(),
            superSpark = superSpark?.toUi(),
            superSparkBalance = superSpark?.purchasedBalance?.coerceAtLeast(0) ?: 0,
            // A mechanic switched off takes its pane with it.
            rewindLimit = rewindLimit.takeIf { rewind != null },
            superSparkLimit = superSparkLimit.takeIf { superSpark != null },
        )
    }

    private suspend fun load(refill: Boolean = false) {
        when (val result = repository.pulseToday()) {
            is DatingResult.Success -> {
                // cohort_gated lives in meta now, and at the top level on the
                // older shape; PulseTodayDto.gated reads whichever says yes.
                list.gated.value = result.value.gated
                list.failure.value = null
                list.rows.value = result.value.data
                remainingAtLastMeta = result.value.meta?.remainingToday?.coerceAtLeast(0) ?: 0
                // A fresh batch: nobody in it is on their way out, unless an
                // action is still in flight and this is the answer overtaking it.
                _deck.update { current ->
                    result.value.meta.onto(current).copy(
                        refilling = false,
                        leaving = current.leaving.takeIf { _busy.value != null },
                    )
                }
            }
            is DatingResult.Failure -> {
                list.failure.value = DatingCopy.forError(result.error)
                _deck.update { it.copy(refilling = false) }
                // The stack is empty but not failed-to-load, so the pane would
                // read "all caught up": say what actually happened.
                if (refill) _message.value = DatingCopy.message(result.error)
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun dismissCelebration() = celebrations.dismiss()

    fun sayHello() = celebrations.sayHello()

    fun helloHandled() = celebrations.helloHandled()

    fun dismissSparkLimit() {
        _deck.update { it.copy(sparkLimit = null) }
    }

    fun dismissSuperSparkLimit() {
        _deck.update { it.copy(superSparkLimit = null) }
    }

    fun dismissRewindLimit() {
        _deck.update { it.copy(rewindLimit = null) }
    }

    fun spark(userId: String, note: String? = null): Boolean =
        act(userId, DeckExit.SPARK) { sendSpark(userId, note, superSpark = false) }

    /**
     * A Super Spark (mechanic M3): the button and the upward swipe. Refused
     * without asking the server while the mechanic is off — absent from the
     * allowances read — which is also when the screen draws neither.
     */
    fun superSpark(userId: String): Boolean {
        if (!_deck.value.superSparkEnabled || userId.isBlank()) return false
        return act(userId, DeckExit.SUPER_SPARK) { sendSpark(userId, note = null, superSpark = true) }
    }

    private suspend fun sendSpark(userId: String, note: String?, superSpark: Boolean) {
        when (val result = repository.spark(userId, note, superSpark)) {
            is DatingResult.Success -> {
                // The name and photo come from the card being sparked, which is on screen.
                val card = list.rows.value?.firstOrNull { it.profile.userId == userId }?.profile
                acted(userId)
                _deck.update { if (superSpark) it.spentSuperSpark().copy(rewindable = null) else it.copy(rewindable = null) }
                val created = result.value
                if (created.matched && created.matchId != null) {
                    celebrations.show(
                        matchId = created.matchId,
                        name = card?.firstName.orEmpty(),
                        photoUrl = urls.forViewer(card?.primaryPhotoUrl, matched = false),
                    )
                } else {
                    _message.value = successMessage(if (superSpark) "Super Spark sent. You'll be first in their sparks." else "Spark sent.")
                }
            }
            is DatingResult.Failure -> sparkRefused(userId, result.error, superSpark)
        }
        refreshAllowances()
    }

    private fun sparkRefused(userId: String, error: DatingError, superSpark: Boolean) {
        val limit = { error.detailsAs(repository.json, RateLimitDetailsDto.serializer()).toUi() }
        when {
            // Each allowance has its own pane, not a line: the card comes back and stays.
            error.code == CODE_SPARK_RATE_LIMITED -> _deck.update { it.copy(sparkLimit = limit()) }
            superSpark && error.code == CODE_SUPER_SPARK_LIMIT -> _deck.update {
                // The daily allowance is used AND no pack Super Spark is left.
                it.copy(superSparkLimit = limit(), superSpark = it.superSpark?.copy(remaining = 0), superSparkBalance = 0)
            }
            superSpark && error.code == CODE_MECHANIC_NOT_ENABLED -> {
                session.disableMechanic(MECHANIC_SUPER_SPARK)
                _deck.update { it.copy(superSpark = null, superSparkBalance = 0, superSparkLimit = null) }
                _message.value = infoMessage("Super Spark isn't available right now.")
            }
            else -> refused(userId, error)
        }
    }

    /** One Super Spark spent locally, daily allowance first and then a pack's, until the server's read replaces it. */
    private fun DeckUi.spentSuperSpark(): DeckUi {
        val daily = superSpark ?: return this
        return when {
            daily.unlimited -> this
            daily.remaining > 0 -> copy(superSpark = daily.copy(remaining = daily.remaining - 1))
            else -> copy(superSparkBalance = (superSparkBalance - 1).coerceAtLeast(0))
        }
    }

    fun pass(userId: String): Boolean = act(userId, DeckExit.PASS) {
        when (val result = repository.pass(userId)) {
            is DatingResult.Success -> {
                acted(userId)
                // The one action undo can take back, until the next one.
                _deck.update { it.copy(rewindable = userId) }
                refreshAllowances()
            }
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    fun stash(userId: String): Boolean = act(userId, DeckExit.STASH) {
        when (val result = repository.stash(userId)) {
            is DatingResult.Success -> {
                // Saving is not a decision: the server does not count it.
                list.drop(userId)
                _deck.update { it.copy(rewindable = null) }
                _message.value = successMessage("Saved for later.")
            }
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    /**
     * Undoes the last pass (mechanic M2). False when nothing was started: the
     * control is not offered, or another action is in flight.
     *
     * The server's card goes back on top; without one the deck is refetched,
     * and the person comes back with it.
     */
    fun rewind(): Boolean {
        val passed = _deck.value.rewindable
        if (!_deck.value.canRewind || passed == null || _busy.value != null) return false
        _busy.value = passed
        viewModelScope.launch {
            try {
                when (val result = repository.rewind()) {
                    is DatingResult.Success -> restore(result.value)
                    is DatingResult.Failure -> rewindRefused(result.error)
                }
            } finally {
                _busy.value = null
            }
            refreshAllowances()
        }
        return true
    }

    private suspend fun restore(rewound: RewindDto) {
        _deck.update { current ->
            current.copy(
                rewindable = null,
                // The card left as a pass and keeps that marker; it must not
                // fly straight back out when it is drawn again.
                leaving = null,
                rewind = current.rewind?.let { rewound.allowance.toUi() },
                // The server gives the card back to today's allowance.
                remaining = if (current.metered) (current.remaining + 1).coerceAtMost(current.dailyLimit) else current.remaining,
            )
        }
        val card = rewound.card
        if (card != null) {
            list.failure.value = null
            list.rows.update { rows -> listOf(card) + rows.orEmpty().filterNot { it.profile.userId == card.profile.userId } }
        } else {
            load()
        }
        _message.value = successMessage("Pass undone.")
    }

    private fun rewindRefused(error: DatingError) {
        when (error.code) {
            CODE_REWIND_LIMIT -> {
                val details = error.detailsAs(repository.json, RateLimitDetailsDto.serializer())
                // The pass is still there to undo once undos come back or a pass is bought.
                _deck.update { it.copy(rewindLimit = details.toUi(), rewind = it.rewind?.copy(remaining = 0)) }
            }
            CODE_NOTHING_TO_UNDO -> _deck.update { it.copy(rewindable = null) }
            CODE_MECHANIC_NOT_ENABLED -> {
                session.disableMechanic(MECHANIC_REWIND)
                _deck.update { it.copy(rewind = null, rewindable = null, rewindLimit = null) }
            }
            CODE_CANDIDATE_UNAVAILABLE -> {
                // Blocked or gone: there is no one to bring back, and it cost nothing.
                _deck.update { it.copy(rewindable = null) }
                _message.value = DatingCopy.message(error)
            }
            else -> _message.value = DatingCopy.message(error, repository.json)
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

    /** A spark or a pass the server took: the card goes, and it counts against today's allowance. */
    private fun acted(userId: String) {
        list.drop(userId)
        _deck.update { if (it.metered) it.copy(remaining = (it.remaining - 1).coerceAtLeast(0)) else it }
    }

    private fun refused(userId: String, error: DatingError) {
        if (error.code == CODE_CANDIDATE_UNAVAILABLE) list.drop(userId)
        _message.value = DatingCopy.message(error, repository.json)
    }

    /** Runs [block] as the one action in flight. False when another already is, and nothing was started. */
    private fun act(userId: String, exit: DeckExit? = null, block: suspend () -> Unit): Boolean {
        if (_busy.value != null) return false
        _busy.value = userId
        _deck.update { it.copy(leaving = exit?.let { way -> DeckLeaving(userId, way) }) }
        viewModelScope.launch {
            try {
                block()
            } finally {
                // Still in the deck means the server refused: the card comes
                // back. A card that left keeps its marker — it is no longer
                // drawn, and clearing it first would start it back for a frame.
                if (inDeck(userId)) _deck.update { it.copy(leaving = null) }
                _busy.value = null
            }
            refillIfEmpty()
        }
        return true
    }

    private fun visibleRows(): List<PulseCardDto> =
        list.rows.value.orEmpty().filterNot { it.profile.userId in session.removed.value }

    private fun inDeck(userId: String): Boolean = visibleRows().any { it.profile.userId == userId }

    /** The refetch rule. See the class comment. */
    private suspend fun refillIfEmpty() {
        if (!_deck.value.metered || remainingAtLastMeta <= 0) return
        if (list.rows.value == null || visibleRows().isNotEmpty()) return
        _deck.update { it.copy(refilling = true) }
        load(refill = true)
    }

    private fun PulseCardDto.toUi(): CardUi = toCardUi(urls)

    /**
     * Mechanic M8: whether travel is offered and the trip in effect, for the
     * top bar's entry and the deck's banner. Off for the session once the
     * server answers `MECHANIC_NOT_ENABLED`; a failed read changes nothing.
     */
    private suspend fun loadTravel() {
        if (session.isMechanicDisabled(TravelRules.MECHANIC)) {
            _deck.update { it.copy(travelEnabled = false, trip = null) }
            return
        }
        when (val result = repository.travel()) {
            is DatingResult.Success -> _deck.update { it.copy(travelEnabled = true, trip = TravelRules.trip(result.value.active)) }
            is DatingResult.Failure -> if (result.error.code == CODE_MECHANIC_NOT_ENABLED) {
                session.disableMechanic(TravelRules.MECHANIC)
                _deck.update { it.copy(travelEnabled = false, trip = null) }
            }
        }
    }

    private companion object {
        const val CODE_SPARK_RATE_LIMITED = "SPARK_RATE_LIMITED"
        const val CODE_SUPER_SPARK_LIMIT = "SUPER_SPARK_LIMIT_REACHED"
        const val CODE_REWIND_LIMIT = "REWIND_LIMIT_REACHED"
        const val CODE_NOTHING_TO_UNDO = "REWIND_NOTHING_TO_UNDO"
        const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        const val CODE_CANDIDATE_UNAVAILABLE = "CANDIDATE_UNAVAILABLE"
        const val MECHANIC_REWIND = "rewind"
        const val MECHANIC_SUPER_SPARK = "super_spark"
    }
}

internal fun reportFailure(error: DatingError): UsMessage =
    if (error is DatingError.Unexpected && error.status == null && !error.message.isNullOrBlank()) {
        errorMessage(error.message)
    } else {
        DatingCopy.message(error)
    }

data class IncomingSparkUi(
    val sparkId: String,
    val fromUserId: String,
    val name: String?,
    val age: Int?,
    /** A city name, or null. Never coordinates. */
    val city: String?,
    /** An intent label ("Casual"), or null for blank and unknown codes. */
    val intent: String?,
    /** A bucket label, or null when the server sent no bucket. Never a number. */
    val distance: String?,
    val verified: Boolean,
    val photoUrl: String?,
    val note: String?,
    /** The same pre-match block the deck shows: a spark is decided on here too. */
    val detail: PersonDetailUi? = null,
    /** A Super Spark: marked on the row. The server already lists these first, and the app keeps its order. */
    val superSpark: Boolean = false,
    /** Mechanic M8: "Visiting Pune" while they are on a trip, else null. */
    val visiting: String? = null,
)

/** Incoming sparks: accept (through the accept route) or decline. */
@HiltViewModel
class SparksViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val safety: SafetyActions,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val list = RemovableList<SparkDto> { it.fromUserId }

    val state: StateFlow<ListState<IncomingSparkUi>> =
        list.state(session) { it.toUi() }.stateIn(viewModelScope, SharingStarted.Eagerly, ListState.Loading)

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val celebrations = MatchCelebrations(repository, urls, viewModelScope)
    val celebration: StateFlow<MatchCelebration?> = celebrations.state
    val hello: StateFlow<HelloTarget?> = celebrations.hello

    init {
        refresh()
    }

    fun sayHello() = celebrations.sayHello()

    fun helloHandled() = celebrations.helloHandled()

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.incomingSparks()) {
                is DatingResult.Success -> {
                    list.failure.value = null
                    // A LOCKED row (mechanic M4) names no sender: nothing on it
                    // can be accepted, reported or shown, so this list leaves it
                    // out. The "liked you" grid is where locked sparks appear.
                    list.rows.value = result.value.filterNot { it.locked || it.fromUserId.isBlank() }
                }
                is DatingResult.Failure -> list.failure.value = DatingCopy.forError(result.error)
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun dismissCelebration() = celebrations.dismiss()

    /** Accepts BY SPARK ID, so the server pairs it with the spark that was sent. */
    fun accept(spark: IncomingSparkUi) {
        viewModelScope.launch {
            when (val result = repository.acceptSpark(spark.sparkId)) {
                is DatingResult.Success -> {
                    list.drop(spark.fromUserId)
                    val created = result.value
                    val matchId = created.matchId?.takeIf { created.matched }
                    if (matchId != null) {
                        celebrations.show(matchId, spark.name.orEmpty(), spark.photoUrl)
                    } else {
                        _message.value = successMessage("Spark sent back. Your match will appear soon.")
                    }
                }
                is DatingResult.Failure -> {
                    // Gone either way: already declined (404) or the person left.
                    val gone = result.error.code == "CANDIDATE_UNAVAILABLE" ||
                        (result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND
                    if (gone) list.drop(spark.fromUserId)
                    _message.value = DatingCopy.message(result.error)
                }
            }
        }
    }

    fun decline(spark: IncomingSparkUi) {
        viewModelScope.launch {
            when (val result = repository.declineSpark(spark.sparkId)) {
                is DatingResult.Success -> list.drop(spark.fromUserId)
                is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
            }
        }
    }

    fun block(userId: String) {
        viewModelScope.launch {
            when (val result = safety.block(userId)) {
                is DatingResult.Success -> _message.value = successMessage("Blocked.")
                is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
            }
        }
    }

    fun report(draft: ReportDraft) {
        viewModelScope.launch {
            when (val result = safety.report(draft)) {
                is DatingResult.Success -> _message.value = successMessage("Thanks for telling us. We've blocked them for you.")
                is DatingResult.Failure -> _message.value = reportFailure(result.error)
            }
        }
    }

    private fun SparkDto.toUi(): IncomingSparkUi =
        incomingSparkUi(sparkId = id, fromUserId = fromUserId, person = person, note = note, superSpark = superSpark, urls = urls)

    private companion object {
        const val HTTP_NOT_FOUND = 404
    }
}

/**
 * An incoming spark in display terms, from the sender's person card. Shared by
 * the incoming list and the unlocked "liked you" grid, so the two can never
 * describe the same person differently.
 */
internal fun incomingSparkUi(
    sparkId: String,
    fromUserId: String,
    person: DatingPersonDto?,
    note: String?,
    superSpark: Boolean,
    urls: DatingPhotoUrls,
    photoUrl: String? = urls.forPerson(person),
): IncomingSparkUi = IncomingSparkUi(
    sparkId = sparkId,
    fromUserId = fromUserId,
    name = person?.firstName?.takeIf { it.isNotBlank() },
    age = person?.age?.takeIf { it > 0 },
    city = person?.city?.trim()?.takeIf { it.isNotBlank() },
    intent = DatingIntent.labelFor(person?.intent),
    distance = DistanceBucket.labelFor(person?.distanceBucket),
    verified = person?.verified == true,
    photoUrl = photoUrl,
    note = note?.takeIf { it.isNotBlank() },
    detail = person?.detail.toUi(urls),
    superSpark = superSpark,
    visiting = visitingLabel(person?.travelling == true, person?.city),
)

data class MatchUi(
    val matchId: String,
    val otherUserId: String,
    val name: String?,
    val age: Int?,
    /** A city name, or null. Never coordinates. */
    val city: String?,
    /** A bucket label, or null when the server sent no bucket. Never a number. */
    val distance: String?,
    /** The server's coarse label, or null when they hide last active — which renders nothing. */
    val lastActive: String?,
    val verified: Boolean,
    /** The variant the card's `photo_state` allows — never upgraded by the app. */
    val photoUrl: String?,
    val status: String,
    val conversationId: String?,
    val expiresAt: String?,
    /** Mechanic M5: set while the match waits for its first message under the rule. */
    val firstMove: FirstMoveUi? = null,
)

/** One match row, from the person the SERVER resolved for this viewer. */
private fun MatchDto.toUi(other: String, urls: DatingPhotoUrls) = MatchUi(
    matchId = id,
    otherUserId = other,
    name = person?.firstName?.takeIf { it.isNotBlank() },
    age = person?.age?.takeIf { it > 0 },
    city = person?.city?.trim()?.takeIf { it.isNotBlank() },
    distance = DistanceBucket.labelFor(person?.distanceBucket),
    lastActive = person?.lastActiveLabel?.trim()?.takeIf { it.isNotBlank() },
    verified = person?.verified == true,
    photoUrl = urls.forPerson(person),
    status = status,
    conversationId = conversationId,
    expiresAt = expiresAt,
    firstMove = firstMove.toUi(),
)

/** The matches list. Closed matches are not shown; blocked people are filtered through the session. */
@HiltViewModel
class MatchesViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    // The server names the other person on every row now; otherOf is the
    // fallback for a row that somehow carries no card.
    private val list = RemovableList<MatchDto> { it.person?.userId ?: session.otherOf(it.userA, it.userB) }

    val state: StateFlow<ListState<MatchUi>> =
        list.state(session) { it.toUi() }.stateIn(viewModelScope, SharingStarted.Eagerly, ListState.Loading)

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.matches()) {
                is DatingResult.Success -> {
                    list.failure.value = null
                    list.rows.value = result.value.filterNot { it.status == STATUS_CLOSED }
                }
                is DatingResult.Failure -> list.failure.value = DatingCopy.forError(result.error)
            }
        }
    }

    private fun MatchDto.toUi(): MatchUi = toUi(person?.userId ?: session.otherOf(userA, userB), urls)

    companion object {
        const val STATUS_CLOSED = "closed"
    }
}

sealed interface MatchDetailState {
    data object Loading : MatchDetailState

    data class Loaded(val match: MatchUi) : MatchDetailState

    /** Unmatched, blocked, reported, or no longer visible. */
    data class Gone(val message: String) : MatchDetailState
}

/** A chat the screen should open once. */
data class ChatRequest(val conversationId: String, val title: String)

/**
 * One match: open its chat (created server-side), unmatch, block, report.
 *
 * On a first-move match (mechanic M5) the person who writes first opens the
 * chat as usual; the person waiting answers an opening question — which the
 * server posts as the first message — or takes the free 24-hour extend.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class MatchDetailViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val safety: SafetyActions,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    private val matchId: String = checkNotNull(savedStateHandle[ARG_MATCH_ID]) { "a match route needs a matchId" }
    private var openChatOnLoad: Boolean = savedStateHandle[ARG_OPEN_CHAT] ?: false

    private val _state = MutableStateFlow<MatchDetailState>(MatchDetailState.Loading)
    val state: StateFlow<MatchDetailState> = _state.asStateFlow()

    private val _chat = MutableStateFlow<ChatRequest?>(null)
    val chat: StateFlow<ChatRequest?> = _chat.asStateFlow()

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    /** Mechanic M5: the answer composer and the free extend. */
    private val _firstMove = MutableStateFlow(FirstMoveActionsUi())
    val firstMove: StateFlow<FirstMoveActionsUi> = _firstMove.asStateFlow()

    /** This person's opening answer went through: the first-move rule is over for them. */
    private var answered = false
    private var answeredConversation: String? = null

    init {
        viewModelScope.launch {
            if (session.myUserId == null) {
                (repository.profile() as? DatingResult.Success)?.value?.let(session::setProfile)
            }
            load()
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun chatOpened() {
        _chat.value = null
    }

    fun openChat() {
        val match = (_state.value as? MatchDetailState.Loaded)?.match ?: return
        if (match.firstMove?.waiting == true) {
            // Chat would refuse this person's first message (FIRST_MOVE_PENDING).
            _message.value = infoMessage("They start this one. Answer one of their questions here, or wait for their message.")
            return
        }
        val conversationId = match.conversationId
        if (conversationId == null) {
            _message.value = errorMessage("Your chat is still being set up. Try again in a moment.")
            viewModelScope.launch { load() }
            return
        }
        _chat.value = ChatRequest(conversationId, match.name ?: "Match")
    }

    fun unmatch() {
        viewModelScope.launch {
            when (val result = repository.unmatch(matchId)) {
                is DatingResult.Success -> _state.value = MatchDetailState.Gone("Unmatched.")
                is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
            }
        }
    }

    fun block() {
        val other = (_state.value as? MatchDetailState.Loaded)?.match?.otherUserId ?: return
        viewModelScope.launch {
            when (val result = safety.block(other)) {
                is DatingResult.Success -> _state.value = MatchDetailState.Gone("Blocked. You won't see each other again.")
                is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
            }
        }
    }

    fun report(draft: ReportDraft) {
        viewModelScope.launch {
            when (val result = safety.report(draft)) {
                is DatingResult.Success -> _state.value = MatchDetailState.Gone("Thanks for telling us. We've blocked them for you.")
                is DatingResult.Failure -> _message.value = reportFailure(result.error)
            }
        }
    }

    // ── Mechanic M5: the waiting person's answer and free extend ────────────

    /** Opens the answer composer under [questionId]. A draft for the same question is kept. */
    fun startAnswer(questionId: String) {
        if (_firstMove.value.sending) return
        _firstMove.update {
            if (it.answeringId == questionId) it else it.copy(answeringId = questionId, answer = "", answerError = null)
        }
    }

    fun editAnswer(text: String) {
        _firstMove.update { it.copy(answer = text.take(FirstMoveCopy.MAX_ANSWER_LENGTH), answerError = null) }
    }

    fun cancelAnswer() {
        if (_firstMove.value.sending) return
        _firstMove.update { it.copy(answeringId = null, answer = "", answerError = null) }
    }

    /**
     * Sends the answer; the server posts it as the chat's first message. From
     * then on this is an ordinary match and "Open chat" is the way in, even if
     * a reload still lists the rule while the server catches up.
     */
    fun sendAnswer() {
        val actions = _firstMove.value
        val questionId = actions.answeringId ?: return
        if (actions.sending) return
        val text = actions.answer.trim()
        if (text.isEmpty()) {
            _firstMove.update { it.copy(answerError = FirstMoveCopy.ANSWER_EMPTY) }
            return
        }
        _firstMove.update { it.copy(sending = true, answerError = null) }
        viewModelScope.launch {
            when (val result = repository.openingAnswer(matchId, questionId, text)) {
                is DatingResult.Success -> {
                    answered = true
                    answeredConversation = result.value.conversationId?.takeIf { it.isNotBlank() }
                    _firstMove.value = FirstMoveActionsUi()
                    _state.update { s -> if (s is MatchDetailState.Loaded) s.copy(match = s.match.answered()) else s }
                    _message.value = successMessage(FirstMoveCopy.ANSWER_SENT)
                    load()
                }
                is DatingResult.Failure -> answerRefused(result.error)
            }
        }
    }

    private suspend fun answerRefused(error: DatingError) {
        when (error.code) {
            // The answer itself: the words go under the field and the draft stays.
            CODE_ANSWER_INVALID, CODE_ANSWER_REFUSED ->
                _firstMove.update { it.copy(sending = false, answerError = DatingCopy.forError(error, repository.json)) }
            // The match moved on, or the question went: read it again.
            CODE_NOT_PENDING, CODE_QUESTION_UNKNOWN, CODE_MECHANIC_NOT_ENABLED -> {
                _firstMove.update { FirstMoveActionsUi(extendLimit = it.extendLimit) }
                _message.value = DatingCopy.message(error, repository.json)
                load()
            }
            else -> if (isGone(error)) {
                _state.value = MatchDetailState.Gone(GONE)
            } else {
                // CHAT_UNAVAILABLE and the network: the draft stays for another try.
                _firstMove.update { it.copy(sending = false) }
                _message.value = DatingCopy.message(error, repository.json)
            }
        }
    }

    /** The free 24 hours for the person waiting on a first-move match. */
    fun extend() {
        val move = (_state.value as? MatchDetailState.Loaded)?.match?.firstMove ?: return
        if (!move.canExtend || _firstMove.value.extending) return
        _firstMove.update { it.copy(extending = true) }
        viewModelScope.launch {
            when (val result = repository.extendMatch(matchId)) {
                is DatingResult.Success -> {
                    val extended = result.value
                    _firstMove.update { it.copy(extending = false, extendLimit = null) }
                    updateFirstMove { it.copy(canExtend = false, deadline = parseInstant(extended.expiresAt) ?: it.deadline) }
                    _message.value = successMessage(FirstMoveCopy.extended(extended.free, extended.extraHours, extended.extraDays))
                    load()
                }
                is DatingResult.Failure -> extendRefused(result.error)
            }
        }
    }

    private fun extendRefused(error: DatingError) {
        when {
            error.code == CODE_EXTEND_LIMIT -> {
                val limit = error.detailsAs(repository.json, RateLimitDetailsDto.serializer()).toUi()
                _firstMove.update { it.copy(extending = false, extendLimit = limit) }
                updateFirstMove { it.copy(canExtend = false) }
            }
            isGone(error) -> _state.value = MatchDetailState.Gone(GONE)
            else -> {
                _firstMove.update { it.copy(extending = false) }
                _message.value = DatingCopy.message(error, repository.json)
            }
        }
    }

    private fun updateFirstMove(transform: (FirstMoveUi) -> FirstMoveUi) {
        _state.update { s ->
            val move = (s as? MatchDetailState.Loaded)?.match?.firstMove
            if (s is MatchDetailState.Loaded && move != null) s.copy(match = s.match.copy(firstMove = transform(move))) else s
        }
    }

    /** A dating-service 404 on this match: blocked, closed or gone. */
    private fun isGone(error: DatingError): Boolean =
        error is DatingError.Refused && error.status == HTTP_NOT_FOUND && error.code == CODE_NOT_FOUND

    /** The match once this person's answer was sent: the rule is over for them. */
    private fun MatchUi.answered(): MatchUi = copy(firstMove = null, conversationId = conversationId ?: answeredConversation)

    private suspend fun load() {
        when (val result = repository.match(matchId)) {
            is DatingResult.Success -> {
                val dto = result.value
                val other = dto.person?.userId ?: session.otherOf(dto.userA, dto.userB)
                if (dto.status == MatchesViewModel.STATUS_CLOSED || session.isRemoved(other)) {
                    _state.value = MatchDetailState.Gone("This match has ended.")
                    return
                }
                val ui = dto.toUi(other, urls).let { if (answered) it.answered() else it }
                _state.value = MatchDetailState.Loaded(ui)
                // The free extend is back: its "spent" line goes.
                if (ui.firstMove?.canExtend == true) _firstMove.update { it.copy(extendLimit = null) }
                if (openChatOnLoad && ui.firstMove?.waiting == true) {
                    // A push for a match they cannot write in yet: stay here, by the questions.
                    openChatOnLoad = false
                } else if (openChatOnLoad && ui.conversationId != null) {
                    openChatOnLoad = false
                    _chat.value = ChatRequest(ui.conversationId, ui.name ?: "Match")
                }
            }
            is DatingResult.Failure -> _state.value = MatchDetailState.Gone(
                if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) GONE else DatingCopy.forError(result.error),
            )
        }
    }

    companion object {
        const val ARG_MATCH_ID = "matchId"
        const val ARG_OPEN_CHAT = "openChat"
        private const val HTTP_NOT_FOUND = 404
        private const val GONE = "This match isn't available any more."
        private const val CODE_NOT_FOUND = "NOT_FOUND"
        private const val CODE_ANSWER_INVALID = "OPENING_ANSWER_INVALID"
        private const val CODE_ANSWER_REFUSED = "OPENING_ANSWER_REFUSED"
        private const val CODE_NOT_PENDING = "FIRST_MOVE_NOT_PENDING"
        private const val CODE_QUESTION_UNKNOWN = "OPENING_QUESTION_UNKNOWN"
        private const val CODE_MECHANIC_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
        private const val CODE_EXTEND_LIMIT = "EXTEND_LIMIT_REACHED"
    }
}
