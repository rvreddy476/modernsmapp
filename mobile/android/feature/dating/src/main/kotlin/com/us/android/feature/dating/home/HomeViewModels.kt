package com.us.android.feature.dating.home

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.DistanceBucket
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.MatchDto
import com.us.android.feature.dating.network.PulseCardDto
import com.us.android.feature.dating.network.SparkDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.photos.PhotoRules
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.SafetyActions
import com.us.android.feature.dating.ui.errorMessage
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
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
)

sealed interface ListState<out T> {
    data object Loading : ListState<Nothing>

    data class Items<T>(val items: List<T>, val gated: Boolean = false) : ListState<T>

    data class Failed(val message: String) : ListState<Nothing>
}

/** A mutual spark just became a match. */
data class MatchCelebration(val matchId: String, val name: String)

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
 */
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

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    private val _celebration = MutableStateFlow<MatchCelebration?>(null)
    val celebration: StateFlow<MatchCelebration?> = _celebration.asStateFlow()

    private val _busy = MutableStateFlow<String?>(null)
    val busy: StateFlow<String?> = _busy.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.pulseToday()) {
                is DatingResult.Success -> {
                    // cohort_gated lives in meta now, and at the top level on the
                    // older shape; PulseTodayDto.gated reads whichever says yes.
                    list.gated.value = result.value.gated
                    list.failure.value = null
                    list.rows.value = result.value.data
                }
                is DatingResult.Failure -> list.failure.value = DatingCopy.forError(result.error)
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun dismissCelebration() {
        _celebration.value = null
    }

    fun spark(userId: String, note: String? = null) = act(userId) {
        when (val result = repository.spark(userId, note)) {
            is DatingResult.Success -> {
                // The name comes from the card being sparked, which is on screen.
                val name = list.rows.value?.firstOrNull { it.profile.userId == userId }?.profile?.firstName.orEmpty()
                list.drop(userId)
                val created = result.value
                if (created.matched && created.matchId != null) {
                    _celebration.value = MatchCelebration(created.matchId, name)
                } else {
                    _message.value = successMessage("Spark sent.")
                }
            }
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    fun pass(userId: String) = act(userId) {
        when (val result = repository.pass(userId)) {
            is DatingResult.Success -> list.drop(userId)
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    fun stash(userId: String) = act(userId) {
        when (val result = repository.stash(userId)) {
            is DatingResult.Success -> {
                list.drop(userId)
                _message.value = successMessage("Saved for later.")
            }
            is DatingResult.Failure -> refused(userId, result.error)
        }
    }

    fun block(userId: String) = act(userId) {
        when (val result = safety.block(userId)) {
            is DatingResult.Success -> _message.value = successMessage("Blocked. You won't see each other again.")
            is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
        }
    }

    fun report(draft: ReportDraft) = act(draft.targetId) {
        when (val result = safety.report(draft)) {
            is DatingResult.Success -> _message.value = successMessage("Thanks for telling us. We've blocked them for you.")
            is DatingResult.Failure -> _message.value = reportFailure(result.error)
        }
    }

    private fun refused(userId: String, error: DatingError) {
        if (error.code == "CANDIDATE_UNAVAILABLE") list.drop(userId)
        _message.value = DatingCopy.message(error, repository.json)
    }

    private fun act(userId: String, block: suspend () -> Unit) {
        if (_busy.value != null) return
        _busy.value = userId
        viewModelScope.launch {
            try {
                block()
            } finally {
                _busy.value = null
            }
        }
    }

    private fun PulseCardDto.toUi(): CardUi = CardUi(
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
    )
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
    /** A bucket label, or null when the server sent no bucket. Never a number. */
    val distance: String?,
    val verified: Boolean,
    val photoUrl: String?,
    val note: String?,
    /** The same pre-match block the deck shows: a spark is decided on here too. */
    val detail: PersonDetailUi? = null,
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

    private val _celebration = MutableStateFlow<MatchCelebration?>(null)
    val celebration: StateFlow<MatchCelebration?> = _celebration.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch {
            when (val result = repository.incomingSparks()) {
                is DatingResult.Success -> {
                    list.failure.value = null
                    list.rows.value = result.value
                }
                is DatingResult.Failure -> list.failure.value = DatingCopy.forError(result.error)
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun dismissCelebration() {
        _celebration.value = null
    }

    /** Accepts BY SPARK ID, so the server pairs it with the spark that was sent. */
    fun accept(spark: IncomingSparkUi) {
        viewModelScope.launch {
            when (val result = repository.acceptSpark(spark.sparkId)) {
                is DatingResult.Success -> {
                    list.drop(spark.fromUserId)
                    val created = result.value
                    _celebration.value = created.matchId?.takeIf { created.matched }?.let { MatchCelebration(it, spark.name.orEmpty()) }
                    if (_celebration.value == null) _message.value = successMessage("Spark sent back. Your match will appear soon.")
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

    private fun SparkDto.toUi(): IncomingSparkUi = IncomingSparkUi(
        sparkId = id,
        fromUserId = fromUserId,
        name = person?.firstName?.takeIf { it.isNotBlank() },
        age = person?.age?.takeIf { it > 0 },
        distance = DistanceBucket.labelFor(person?.distanceBucket),
        verified = person?.verified == true,
        photoUrl = urls.forPerson(person),
        note = note?.takeIf { it.isNotBlank() },
        detail = person?.detail.toUi(urls),
    )

    private companion object {
        const val HTTP_NOT_FOUND = 404
    }
}

data class MatchUi(
    val matchId: String,
    val otherUserId: String,
    val name: String?,
    val age: Int?,
    /** A bucket label, or null when the server sent no bucket. Never a number. */
    val distance: String?,
    val verified: Boolean,
    /** The variant the card's `photo_state` allows — never upgraded by the app. */
    val photoUrl: String?,
    val status: String,
    val conversationId: String?,
    val expiresAt: String?,
)

/** One match row, from the person the SERVER resolved for this viewer. */
private fun MatchDto.toUi(other: String, urls: DatingPhotoUrls) = MatchUi(
    matchId = id,
    otherUserId = other,
    name = person?.firstName?.takeIf { it.isNotBlank() },
    age = person?.age?.takeIf { it > 0 },
    distance = DistanceBucket.labelFor(person?.distanceBucket),
    verified = person?.verified == true,
    photoUrl = urls.forPerson(person),
    status = status,
    conversationId = conversationId,
    expiresAt = expiresAt,
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

/** One match: open its chat (created server-side), unmatch, block, report. */
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

    private suspend fun load() {
        when (val result = repository.match(matchId)) {
            is DatingResult.Success -> {
                val dto = result.value
                val other = dto.person?.userId ?: session.otherOf(dto.userA, dto.userB)
                if (dto.status == MatchesViewModel.STATUS_CLOSED || session.isRemoved(other)) {
                    _state.value = MatchDetailState.Gone("This match has ended.")
                    return
                }
                val ui = dto.toUi(other, urls)
                _state.value = MatchDetailState.Loaded(ui)
                if (openChatOnLoad && ui.conversationId != null) {
                    openChatOnLoad = false
                    _chat.value = ChatRequest(ui.conversationId, ui.name ?: "Match")
                }
            }
            is DatingResult.Failure -> _state.value = MatchDetailState.Gone(
                if ((result.error as? DatingError.Refused)?.status == HTTP_NOT_FOUND) "This match isn't available any more." else DatingCopy.forError(result.error),
            )
        }
    }

    companion object {
        const val ARG_MATCH_ID = "matchId"
        const val ARG_OPEN_CHAT = "openChat"
        private const val HTTP_NOT_FOUND = 404
    }
}
