package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedSurface
import com.us.android.feature.feed.data.FeedRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

@HiltViewModel
class FeedViewModel @Inject constructor(
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
) : ViewModel() {

    /**
     * The image to show for a row.
     *
     * Prefers a LADDER variant over the thumbnail: `thumb_150` is 150px and
     * visibly soft at feed width. `original` is excluded by the resolver — it
     * can be arbitrarily large, and a feed that downloads originals burns the
     * reader's data on images it immediately downscales.
     *
     * For video this is the poster frame. The feed never plays; reels does.
     */
    fun posterUrl(item: FeedItem): String? {
        val media = item.media.firstOrNull() ?: return null
        return if (media.kind == VIDEO_KIND) {
            // A video's ladder rungs are VIDEO files. `480p` on a video asset
            // is an mp4, and handing one to an image loader fetches it in full,
            // fails to decode, and shows an empty box — verified on a device,
            // where the request returned 200 and nothing rendered. The only
            // still frame a video has is its thumbnail.
            urlResolver.thumbnail(media.variants)
        } else {
            urlResolver.bestVariant(media.variants, FEED_IMAGE_MAX_HEIGHT)
                ?: urlResolver.thumbnail(media.variants)
        }
    }

    /**
     * `cachedIn(viewModelScope)` is not optional here.
     *
     * Without it the paging flow is re-collected on every configuration change
     * and every recomposition that resubscribes, which refetches page one and
     * throws away the user's scroll position. With it the loaded pages survive
     * rotation.
     */
    val items: Flow<PagingData<FeedItem>> =
        repository.feed(FeedSurface.Home).cachedIn(viewModelScope)

    /**
     * Optimistic engagement, layered over the immutable page.
     *
     * A PagingData page cannot be edited in place, so a tap cannot be written
     * back into the item it belongs to. The overlay carries the viewer's
     * intent instead, and [EngagementStore] performs the real write — the same
     * store post detail observes, so a like in the feed is already applied
     * when the post opens.
     */
    val overlays: StateFlow<Map<String, EngagementOverlay>> = engagement.overlays

    val failures: StateFlow<List<EngagementFailure>> = engagement.failures

    fun onReact(postId: String, serverReacted: Boolean) = viewModelScope.launch {
        engagement.toggleReaction(postId, serverReacted)
    }

    fun onBookmark(postId: String, serverBookmarked: Boolean) = viewModelScope.launch {
        engagement.toggleBookmark(postId, serverBookmarked)
    }

    fun onRepost(postId: String, serverReposted: Boolean) = viewModelScope.launch {
        engagement.toggleRepost(postId, serverReposted)
    }

    /**
     * Records an external share AFTER the chooser was launched.
     *
     * Fire-and-forget: the share already happened in another app, so a failed
     * count must not produce an error the user cannot act on. Exactly one
     * server event per chooser launch.
     */
    fun onExternalShared(postId: String) = viewModelScope.launch {
        shares.recordExternalShare(postId)
    }

    fun dismissFailure(postId: String, action: EngagementAction) =
        engagement.clearFailure(postId, action)

    fun retryFailure(postId: String, action: EngagementAction) = viewModelScope.launch {
        engagement.retry(postId, action)
    }

    /**
     * Post ids already reconciled for the current refresh generation.
     *
     * The paging snapshot is cumulative: after appending page two it still
     * contains page one's ORIGINAL rows, captured before anything was liked.
     * Those rows are not new server authority, and treating them as such is
     * how a successful like silently undid itself on scroll.
     */
    private val hydratedIds = mutableSetOf<String>()

    /**
     * A refresh generation completed: every row is genuinely fresh.
     *
     * This is the only place old server values may retire a settled overlay,
     * because it is the only place they are known to have just come from the
     * server rather than from a snapshot held since before the tap.
     */
    fun onRefreshHydrated(items: List<FeedItem>) {
        hydratedIds.clear()
        items.forEach { item ->
            hydratedIds += item.id
            reconcile(item)
        }
    }

    /**
     * A page was appended: only rows never seen this generation are new.
     *
     * Everything already in [hydratedIds] is a retained snapshot row. Passing
     * it to the store would hand it a stale `has_reacted=false` and retire a
     * confirmed like — the heart and count visibly reverting while the server
     * still holds the reaction.
     */
    fun onAppendHydrated(items: List<FeedItem>) {
        items.forEach { item ->
            if (hydratedIds.add(item.id)) reconcile(item)
        }
    }

    private fun reconcile(item: FeedItem) = engagement.reconcile(
        postId = item.id,
        serverReacted = item.viewer.hasReacted,
        serverBookmarked = item.viewer.isBookmarked,
        serverReposted = item.viewer.hasReposted,
    )
}

/**
 * Feed rows are at most screen-width, so a 720p rung is already more pixels
 * than a phone can show. Asking for 1080p would triple the bytes for a row
 * that gets scrolled past in under a second.
 */
private const val FEED_IMAGE_MAX_HEIGHT = 720

/** `kind` on a feed media entry; `image` is the other value in use. */
private const val VIDEO_KIND = "video"
