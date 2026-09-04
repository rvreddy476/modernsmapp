package com.us.android.feature.tube.ui.home

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.HiddenPosts
import com.us.android.core.feed.data.FeedRepository
import com.us.android.core.feed.data.hides
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.publish.ReelPublishActions
import com.us.android.core.media.publish.ReelPublishState
import com.us.android.core.media.publish.ReelPublishTracker
import com.us.android.core.media.publish.VideoKind
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedQuery
import com.us.android.feature.tube.data.TubeQueue
import com.us.android.feature.tube.ui.VideoThumb
import com.us.android.feature.tube.ui.videoThumb
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharingStarted
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.stateIn
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * The slot ABOVE the ranked videos: the viewer's own long video while it
 * posts, and the real post the moment the server has created it — the same
 * shape as the Reels head, for the same reason: the item IS the progress.
 */
sealed interface TubeHead {
    /** Still posting (or stopped). The cover the user chose, the title, the caption. */
    data class Pending(
        val creationKey: String,
        val coverPath: String?,
        val title: String,
        val caption: String,
        /** Null while work is in flight; set when the publish stopped. */
        val failure: TubePendingFailure? = null,
    ) : TubeHead

    /** The post exists. Rendered as every other card, and it opens. */
    data class Live(val item: FeedItem) : TubeHead
}

data class TubePendingFailure(val message: String, val retryable: Boolean)

/**
 * Tube home: the `videos` surface as the server ranks it, with the viewer's
 * own long video at the head while it posts (Tube, 2026-09-05).
 *
 * The publish tracker is process-wide and shared with Reels; the two
 * surfaces split it by [VideoKind] — a reel posting never shows here and a
 * long video never shows in Reels — so one worker and one persisted record
 * serve both without either drawing the other's pending item.
 */
@HiltViewModel
// Constructor injection of the surface's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class TubeHomeViewModel @Inject constructor(
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val tracker: ReelPublishTracker,
    private val publishActions: ReelPublishActions,
    private val queue: TubeQueue,
    hidden: HiddenPosts,
) : ViewModel() {

    /** The long video this session just published, once fetched — pinned above the ranked rows. */
    private val _live = MutableStateFlow<FeedItem?>(null)

    /**
     * The ranked videos, one cached stream so rotation replays the pages
     * rather than refetching. A video that went live this session leaves the
     * ranked page once the feed carries it: the head already shows it.
     */
    val items: Flow<PagingData<FeedItem>> = repository.feed(FeedQuery.Videos)
        .cachedIn(viewModelScope)
        .combine(_live) { page, live ->
            if (live == null) page else page.filter { it.id != live.id }
        }
        .combine(hidden.state) { page, set ->
            // "Not interested", Block and Delete from the more sheet — removed
            // at once, the same way every feed removes them.
            if (set.isEmpty) page else page.filter { !set.hides(it) }
        }

    /** The slot above the list: nothing, the pending card, or the live post. */
    val head: StateFlow<TubeHead?> = combine(tracker.state, tracker.preview, _live) { state, preview, live ->
        when {
            live != null -> TubeHead.Live(live)
            preview == null || preview.kind != VideoKind.LONG || state is ReelPublishState.Idle -> null
            else -> TubeHead.Pending(
                creationKey = preview.creationKey,
                coverPath = preview.coverPath,
                title = preview.title,
                caption = preview.caption,
                failure = (state as? ReelPublishState.Failed)?.let { TubePendingFailure(it.message, it.retryable) },
            )
        }
    }.stateIn(viewModelScope, SharingStarted.WhileSubscribed(STOP_TIMEOUT_MILLIS), null)

    init {
        // The moment the worker reports the post id, fetch the post the server
        // made of it and let the tracker go — the pending card becomes the
        // real thing without a refresh.
        viewModelScope.launch {
            tracker.state.collect { state ->
                if (state is ReelPublishState.Published && tracker.preview.value?.kind == VideoKind.LONG) {
                    becomeLive(state.postId)
                }
            }
        }
    }

    private suspend fun becomeLive(postId: String) {
        if (_live.value?.id == postId) return
        repeat(LIVE_FETCH_ATTEMPTS) { attempt ->
            when (val result = repository.post(postId)) {
                is AppResult.Success -> {
                    _live.value = result.data
                    publishActions.dismiss()
                    return
                }
                is AppResult.Failure -> if (attempt < LIVE_FETCH_ATTEMPTS - 1) delay(LIVE_FETCH_RETRY_MILLIS)
            }
        }
        // The post exists even if this client could not read it back yet; the
        // next refresh carries it. A loader over a finished publish would lie.
        publishActions.dismiss()
    }

    fun retryPublish() = publishActions.retry()

    fun discardPublish() = publishActions.discard()

    /** What the card draws before its video: the still, the wash, the length. */
    fun thumb(item: FeedItem): VideoThumb = urlResolver.videoThumb(item)

    /**
     * A card was tapped: the list as the viewer sees it — the live head
     * first, then the loaded rows — becomes what the watch screen plays
     * through, so "Up next" is the rows under the tapped one.
     */
    fun onOpen(loaded: List<FeedItem>) {
        queue.set(listOfNotNull(_live.value) + loaded)
    }

    private companion object {
        const val STOP_TIMEOUT_MILLIS = 5_000L

        /** post-service can lag the worker's answer by a beat; three tries a second apart covers it. */
        const val LIVE_FETCH_ATTEMPTS = 3
        const val LIVE_FETCH_RETRY_MILLIS = 1_000L
    }
}
