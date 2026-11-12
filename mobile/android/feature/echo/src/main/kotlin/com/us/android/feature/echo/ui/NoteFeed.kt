package com.us.android.feature.echo.ui

import androidx.compose.runtime.Immutable
import com.us.android.core.common.result.AppResult
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.Eligibility
import com.us.android.feature.echo.data.EligibilityReason
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NotePage
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.eligibilityWords
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/** One cursor-paged list of notes: a timeline, a note's replies, a profile tab. */
@Immutable
data class NoteFeed(
    val items: List<Note> = emptyList(),
    val cursor: String? = null,
    /** The first page has arrived at least once. */
    val loaded: Boolean = false,
    val loading: Boolean = false,
    val refreshing: Boolean = false,
    val loadingMore: Boolean = false,
    /** The first page failed: the list is replaced by this and Try again. */
    val error: String? = null,
    /** A later page failed: what is shown stays, with Try again at the foot. */
    val moreError: String? = null,
) {
    val canLoadMore: Boolean get() = loaded && cursor != null && !loadingMore && !loading && !refreshing
    val empty: Boolean get() = loaded && items.isEmpty() && error == null

    /** A first page (or a refresh) replaces everything. */
    fun replacedBy(page: NotePage): NoteFeed =
        NoteFeed(items = page.items.distinctBy { it.id }, cursor = page.nextCursor, loaded = true)

    /** A later page joins the end; a note already shown is never repeated. */
    fun appended(page: NotePage): NoteFeed {
        val seen = items.mapTo(HashSet()) { it.id }
        return copy(
            items = items + page.items.filter { seen.add(it.id) },
            cursor = page.nextCursor,
            loadingMore = false,
            moreError = null,
        )
    }
}

/**
 * Drives one [NoteFeed]: the first page, pull to refresh, the next page by
 * the server's cursor. [fetch] is called with null for the first page.
 * Pages that arrive for a list that was refreshed meanwhile are dropped.
 */
class NoteFeedController(
    private val scope: CoroutineScope,
    private val fetch: suspend (cursor: String?) -> AppResult<NotePage>,
    private val onPage: suspend (List<Note>) -> Unit = {},
) {
    private val _feed = MutableStateFlow(NoteFeed())
    val feed: StateFlow<NoteFeed> = _feed.asStateFlow()

    private var job: Job? = null
    private var generation = 0

    /** The first page, unless it is already there or on its way. */
    fun loadIfNeeded() {
        val f = _feed.value
        if (!f.loaded && !f.loading) load(refresh = false)
    }

    fun refresh() = load(refresh = true)

    fun retry() = load(refresh = false)

    private fun load(refresh: Boolean) {
        job?.cancel()
        val gen = ++generation
        _feed.update {
            if (refresh && it.loaded) {
                it.copy(refreshing = true, moreError = null)
            } else {
                it.copy(loading = true, error = null)
            }
        }
        job = scope.launch {
            when (val result = fetch(null)) {
                is AppResult.Success -> {
                    if (gen != generation) return@launch
                    _feed.value = _feed.value.replacedBy(result.data)
                    onPage(result.data.items)
                }
                is AppResult.Failure -> {
                    if (gen != generation) return@launch
                    val message = echoErrorMessage(result.error)
                    _feed.update {
                        // A failed refresh keeps the list that is on screen.
                        if (it.loaded && it.items.isNotEmpty()) {
                            it.copy(refreshing = false, loading = false, moreError = message)
                        } else {
                            it.copy(loading = false, refreshing = false, error = message, loaded = false)
                        }
                    }
                }
            }
        }
    }

    fun loadMore() {
        val f = _feed.value
        if (!f.canLoadMore) return
        val cursor = f.cursor
        val gen = generation
        _feed.update { it.copy(loadingMore = true, moreError = null) }
        job = scope.launch {
            when (val result = fetch(cursor)) {
                is AppResult.Success -> {
                    if (gen != generation) return@launch
                    _feed.update { it.appended(result.data) }
                    onPage(result.data.items)
                }
                is AppResult.Failure -> {
                    if (gen != generation) return@launch
                    _feed.update { it.copy(loadingMore = false, moreError = echoErrorMessage(result.error)) }
                }
            }
        }
    }

    /** Changes every copy of note [id] on this list, quoted ones too. */
    fun mapNote(id: String, transform: (Note) -> Note) {
        _feed.update { it.copy(items = it.items.mapNote(id, transform)) }
    }

    /** A deleted note leaves the list. */
    fun remove(id: String) {
        _feed.update { it.copy(items = it.items.filterNot { note -> note.id == id }) }
    }

    /** A note just posted, at the top. */
    fun prepend(note: Note) {
        _feed.update { f -> f.copy(items = listOf(note) + f.items.filterNot { it.id == note.id }) }
    }
}

/** [this] with every note [id] (and every quote of it) passed through [transform]. */
fun List<Note>.mapNote(id: String, transform: (Note) -> Note): List<Note> = map { it.mapNote(id, transform) }

fun Note.mapNote(id: String, transform: (Note) -> Note): Note {
    val self = if (this.id == id) transform(this) else this
    val quote = self.quoted
    return if (quote != null && quote.id == id) self.copy(quoted = transform(quote)) else self
}

/** The heart on or off, its count moved with it. */
fun Note.withLiked(liked: Boolean): Note =
    if (this.liked == liked) {
        this
    } else {
        copy(liked = liked, likeCount = (likeCount + if (liked) 1 else -1).coerceAtLeast(0))
    }

/** The plain repost on or off, its count moved with it. */
fun Note.withReposted(reposted: Boolean): Note =
    if (this.reposted == reposted) {
        this
    } else {
        copy(reposted = reposted, repostCount = (repostCount + if (reposted) 1 else -1).coerceAtLeast(0))
    }

/**
 * Heart and repost, optimistic: [apply] changes the note on screen at once,
 * and puts it back if the server refuses. Answers the refusal's sentence, or
 * null when it went through.
 */
suspend fun toggleLike(repository: EchoRepository, note: Note, apply: (String, (Note) -> Note) -> Unit): String? {
    val target = !note.liked
    apply(note.id) { it.withLiked(target) }
    val result = repository.setLiked(note.id, target)
    if (result is AppResult.Failure) {
        apply(note.id) { it.withLiked(!target) }
        return echoErrorMessage(result.error)
    }
    return null
}

suspend fun toggleRepost(repository: EchoRepository, note: Note, apply: (String, (Note) -> Note) -> Unit): String? {
    val target = !note.reposted
    apply(note.id) { it.withReposted(target) }
    val result = repository.setReposted(note.id, target)
    if (result is AppResult.Failure) {
        apply(note.id) { it.withReposted(!target) }
        return echoErrorMessage(result.error)
    }
    return null
}

/** What opening the composer comes to, after the eligibility read. */
sealed interface ComposeGate {
    data object Open : ComposeGate

    /** The sheet: why this viewer cannot, in the contract's words. */
    data class Blocked(val title: String, val detail: String) : ComposeGate

    /** The read itself failed: fail closed, with the reason. */
    data class Failed(val message: String) : ComposeGate
}

/**
 * Starting a note, a thread or a quote: Premium only (decision 5). Anything
 * but `can_post: true` keeps the composer shut.
 */
fun postGate(eligibility: Eligibility): ComposeGate =
    if (eligibility.canPost) ComposeGate.Open else blocked(eligibility.reason)

/**
 * Replying: good standing and an account 7 days old (`can_reply`), and the
 * note's own reply policy (`viewer_can_reply`). Either one false keeps the
 * composer shut.
 */
fun replyGate(eligibility: Eligibility, note: Note): ComposeGate = when {
    !eligibility.canReply -> blocked(
        eligibility.reason.takeUnless { it == EligibilityReason.PREMIUM_REQUIRED } ?: EligibilityReason.NONE
    )
    !note.viewerCanReply -> ComposeGate.Blocked(REPLY_LIMITED_TITLE, EchoWords.REPLY_NOT_ALLOWED)
    else -> ComposeGate.Open
}

private fun blocked(reason: EligibilityReason): ComposeGate.Blocked =
    eligibilityWords(reason)?.let { (title, detail) -> ComposeGate.Blocked(title, detail) }
        ?: ComposeGate.Blocked(CANNOT_POST_TITLE, CANNOT_POST_DETAIL)

const val REPLY_LIMITED_TITLE = "Replies are limited"
const val CANNOT_POST_TITLE = "You can't post on Echo right now"
const val CANNOT_POST_DETAIL = "Try again later."

/** Reads eligibility and answers the gate for [rule]. A failed read is [ComposeGate.Failed]. */
suspend fun readGate(repository: EchoRepository, rule: (Eligibility) -> ComposeGate): ComposeGate =
    when (val result = repository.eligibility()) {
        is AppResult.Success -> rule(result.data)
        is AppResult.Failure -> ComposeGate.Failed(echoErrorMessage(result.error))
    }
