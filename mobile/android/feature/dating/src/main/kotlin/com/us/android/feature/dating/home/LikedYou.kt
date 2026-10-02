package com.us.android.feature.dating.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.network.LikedYouDto
import com.us.android.feature.dating.network.LikedYouItemDto
import com.us.android.feature.dating.photos.DatingPhotoUrls
import com.us.android.feature.dating.safety.ReportDraft
import com.us.android.feature.dating.safety.SafetyActions
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

/**
 * One tile of the "liked you" grid (mechanic M4).
 *
 * The two kinds are separate types on purpose: a [Locked] tile has no field
 * that could hold a name, an age, a note or a full photo, so nothing about the
 * sender can reach a locked tile, whatever the server sent.
 */
sealed interface LikedYouTile {
    val sparkId: String
    val superSpark: Boolean

    /** The gate is on and the viewer has no pass: a server-blurred image, and nothing else. */
    data class Locked(
        override val sparkId: String,
        override val superSpark: Boolean,
        /** The blurred-image route only (`/liked-you/<spark_id>/photo`), or null for the placeholder. */
        val photoUrl: String?,
    ) : LikedYouTile

    /** The viewer may see who sparked them: the usual incoming-spark card. */
    data class Open(val spark: IncomingSparkUi) : LikedYouTile {
        override val sparkId: String get() = spark.sparkId
        override val superSpark: Boolean get() = spark.superSpark
    }
}

sealed interface LikedYouState {
    data object Loading : LikedYouState

    data class Failed(val message: String) : LikedYouState

    data class Loaded(
        /** How many people sparked the viewer, across every page. */
        val total: Int,
        /** False while the gate locks the grid: every tile is then [LikedYouTile.Locked]. */
        val unlocked: Boolean,
        /** Super Sparks first, then the server's order. */
        val tiles: List<LikedYouTile>,
        val canLoadMore: Boolean = false,
        val loadingMore: Boolean = false,
    ) : LikedYouState
}

/** The grid's words. Our own. */
object LikedYouCopy {

    /** "3 people sparked you"; null when there is no one, and the empty pane speaks instead. */
    fun header(total: Int): String? = when {
        total <= 0 -> null
        total == 1 -> "1 person sparked you"
        else -> "$total people sparked you"
    }

    const val LOCKED_CTA_TITLE = "See who sparked you with a pass"
    const val LOCKED_CTA_BODY = "Photos stay blurred until you have a Premium pass. With one, you can see everyone and spark them back."
    const val LOCKED_CTA_ACTION = "See Premium"
    const val LOCKED_PILL = "Locked"
    const val UPSELL_TITLE = "See who sparked you"
    const val UPSELL_BODY = "A Premium pass shows you everyone who sparked you, so you can spark them back and match."
    const val EMPTY_TITLE = "No sparks yet"
    const val EMPTY_BODY = "When someone sparks you, they'll show up here."
}

/**
 * The Sparks tab: who sparked the viewer, as a grid (`GET /liked-you`).
 *
 * ## Locked or unlocked
 *
 * The SERVER decides: its `unlocked` flag. With the gate off, or with a pass,
 * it is true and every item carries the sender's person card — the tiles are
 * [LikedYouTile.Open], and Spark back / Decline work as they always have.
 * With the gate on and no pass it is false: every tile is
 * [LikedYouTile.Locked], built from the spark id, the Super Spark flag and the
 * blurred-image route ONLY. A person that somehow arrived on a locked item is
 * ignored, never shown.
 *
 * A locked tile opens the Premium upsell. If accepting ever answers
 * `403 LIKED_YOU_LOCKED` (the pass ran out while the grid was open), the grid
 * locks itself at once — every person leaves the screen — shows the upsell,
 * and reads the server's locked view again.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class LikedYouViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val safety: SafetyActions,
    private val urls: DatingPhotoUrls,
) : ViewModel() {

    /** What the server sent, less what this session declined or accepted since. */
    private data class Page(
        val total: Int,
        val unlocked: Boolean,
        val items: List<LikedYouItemDto>,
        /** The last page fetched was full, so the server may have more. */
        val more: Boolean,
    )

    private val page = MutableStateFlow<Page?>(null)
    private val failure = MutableStateFlow<String?>(null)
    private val loadingMore = MutableStateFlow(false)

    val state: StateFlow<LikedYouState> =
        combine(page, failure, loadingMore, session.removed) { current, fail, more, removed ->
            when {
                current == null && fail != null -> LikedYouState.Failed(fail)
                current == null -> LikedYouState.Loading
                else -> current.toState(removed, more)
            }
        }.stateIn(viewModelScope, SharingStarted.Eagerly, LikedYouState.Loading)

    private val _message = MutableStateFlow<UsMessage?>(null)
    val message: StateFlow<UsMessage?> = _message.asStateFlow()

    /** The Premium upsell is showing: a locked tile was tapped, or accepting met the gate. */
    private val _upsell = MutableStateFlow(false)
    val upsell: StateFlow<Boolean> = _upsell.asStateFlow()

    /** The spark an action is in flight for; the sheet's buttons wait while it is set. */
    private val _busy = MutableStateFlow<String?>(null)
    val busy: StateFlow<String?> = _busy.asStateFlow()

    private val celebrations = MatchCelebrations(repository, urls, viewModelScope)
    val celebration: StateFlow<MatchCelebration?> = celebrations.state
    val hello: StateFlow<HelloTarget?> = celebrations.hello

    init {
        refresh()
    }

    fun refresh() {
        viewModelScope.launch { load() }
    }

    private suspend fun load() {
        when (val result = repository.likedYou(PAGE_SIZE, 0)) {
            is DatingResult.Success -> {
                failure.value = null
                page.value = result.value.toPage()
            }
            is DatingResult.Failure -> failure.value = DatingCopy.forError(result.error)
        }
    }

    /** The next page, when the server may have one. A flip of the lock between pages reloads the whole grid. */
    fun loadMore() {
        val current = page.value ?: return
        if (!current.more || loadingMore.value || current.items.size >= current.total) return
        loadingMore.value = true
        viewModelScope.launch {
            try {
                when (val result = repository.likedYou(PAGE_SIZE, current.items.size)) {
                    is DatingResult.Success -> {
                        val next = result.value
                        val latest = page.value
                        if (latest == null || next.unlocked != latest.unlocked) {
                            load()
                        } else {
                            val known = latest.items.mapTo(HashSet()) { it.sparkId }
                            page.value = latest.copy(
                                total = next.total,
                                items = latest.items + next.items.filterNot { it.sparkId in known },
                                more = next.items.size >= PAGE_SIZE,
                            )
                        }
                    }
                    is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
                }
            } finally {
                loadingMore.value = false
            }
        }
    }

    fun dismissMessage() {
        _message.value = null
    }

    fun showUpsell() {
        _upsell.value = true
    }

    fun dismissUpsell() {
        _upsell.value = false
    }

    fun dismissCelebration() = celebrations.dismiss()

    fun sayHello() = celebrations.sayHello()

    fun helloHandled() = celebrations.helloHandled()

    /** Sparks back BY SPARK ID. Only an open tile can: a locked one names no one to match with. */
    fun accept(tile: LikedYouTile.Open) = act(tile.sparkId) {
        val spark = tile.spark
        when (val result = repository.acceptSpark(spark.sparkId)) {
            is DatingResult.Success -> {
                drop(spark.sparkId)
                val created = result.value
                val matchId = created.matchId?.takeIf { created.matched }
                if (matchId != null) {
                    celebrations.show(matchId, spark.name.orEmpty(), spark.photoUrl)
                } else {
                    _message.value = successMessage("Spark sent back. Your match will appear soon.")
                }
            }
            is DatingResult.Failure -> acceptRefused(spark.sparkId, result.error)
        }
    }

    private suspend fun acceptRefused(sparkId: String, error: DatingError) {
        when {
            error.code == CODE_LIKED_YOU_LOCKED -> {
                // The pass ended while the grid was open: nobody stays on screen.
                lockAll()
                _upsell.value = true
                load()
            }
            // Gone either way: already declined (404) or the person left.
            error.code == CODE_CANDIDATE_UNAVAILABLE || (error as? DatingError.Refused)?.status == HTTP_NOT_FOUND -> {
                drop(sparkId)
                _message.value = DatingCopy.message(error)
            }
            else -> _message.value = DatingCopy.message(error)
        }
    }

    /** Declines an open tile. A locked tile offers no decline: there is no one to decide about. */
    fun decline(tile: LikedYouTile.Open) = act(tile.sparkId) {
        when (val result = repository.declineSpark(tile.sparkId)) {
            is DatingResult.Success -> drop(tile.sparkId)
            is DatingResult.Failure -> _message.value = DatingCopy.message(result.error)
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

    private fun act(sparkId: String, block: suspend () -> Unit): Boolean {
        if (_busy.value != null) return false
        _busy.value = sparkId
        viewModelScope.launch {
            try {
                block()
            } finally {
                _busy.value = null
            }
        }
        return true
    }

    private fun drop(sparkId: String) = page.update { current ->
        current?.let { p ->
            val left = p.items.filterNot { it.sparkId == sparkId }
            p.copy(items = left, total = (p.total - (p.items.size - left.size)).coerceAtLeast(left.size))
        }
    }

    /** Turns every card locked, here and now, keeping only what a locked card may carry. */
    private fun lockAll() = page.update { current ->
        current?.copy(unlocked = false, items = current.items.map { it.copy(person = null, note = null, photoUrl = "") })
    }

    private fun LikedYouDto.toPage() = Page(
        total = total.coerceAtLeast(0),
        unlocked = unlocked,
        // Each spark once: the grid is keyed by spark id.
        items = items.filter { it.sparkId.isNotBlank() }.distinctBy { it.sparkId },
        more = items.size >= PAGE_SIZE,
    )

    private fun Page.toState(removed: Set<String>, more: Boolean): LikedYouState.Loaded {
        val tiles = if (unlocked) items.mapNotNull { it.openTile(removed) } else items.map { it.lockedTile() }
        // Someone blocked or reported in this session leaves the count too.
        val hidden = if (unlocked) items.count { it.person?.userId?.let { id -> id in removed } == true } else 0
        return LikedYouState.Loaded(
            total = (total - hidden).coerceAtLeast(tiles.size),
            unlocked = unlocked,
            // Stable: the server's order is kept within each group.
            tiles = tiles.sortedByDescending { it.superSpark },
            canLoadMore = this.more && items.size < total,
            loadingMore = more,
        )
    }

    /**
     * A locked card from the spark id, the Super Spark flag and the blurred
     * route ONLY. [LikedYouItemDto.person] and [LikedYouItemDto.note] are never
     * read here. A route that is not the blurred one is replaced by the blurred
     * route for this spark, which the server serves blurred whoever asks.
     */
    private fun LikedYouItemDto.lockedTile() = LikedYouTile.Locked(
        sparkId = sparkId,
        superSpark = superSpark,
        photoUrl = urls.forLikedYou(photoUrl) ?: urls.forLikedYouSpark(sparkId),
    )

    /** An open card needs the sender's person card; an item without one has no one to show and is left out. */
    private fun LikedYouItemDto.openTile(removed: Set<String>): LikedYouTile.Open? {
        val sender = person ?: return null
        val userId = sender.userId.takeIf { it.isNotBlank() } ?: return null
        if (userId in removed) return null
        // The card's own photo in the variant its photo_state allows; the item's
        // photo_url (the same route) only when the card carries none.
        val photo = urls.forPerson(sender)
            ?: urls.forPerson(sender.copy(primaryPhotoUrl = photoUrl.takeIf { it.isNotBlank() }))
        return LikedYouTile.Open(
            incomingSparkUi(
                sparkId = sparkId,
                fromUserId = userId,
                person = sender,
                note = note,
                superSpark = superSpark,
                urls = urls,
                photoUrl = photo,
                noteHidden = noteHidden,
            ),
        )
    }

    companion object {
        const val PAGE_SIZE = 50
        const val CODE_LIKED_YOU_LOCKED = "LIKED_YOU_LOCKED"
        private const val CODE_CANDIDATE_UNAVAILABLE = "CANDIDATE_UNAVAILABLE"
        private const val HTTP_NOT_FOUND = 404
    }
}
