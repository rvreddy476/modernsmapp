package com.us.android.feature.feed.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import androidx.paging.filter
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.datastore.SettingsDataStore
import com.us.android.core.engagement.data.EngagementAction
import com.us.android.core.engagement.data.EngagementFailure
import com.us.android.core.engagement.data.EngagementOverlay
import com.us.android.core.engagement.data.EngagementRepository
import com.us.android.core.engagement.data.EngagementStore
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedQuery
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.TrendingHashtag
import com.us.android.core.ui.PostCardMediaPage
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.FollowGraph
import com.us.android.feature.feed.data.KeywordFilter
import dagger.assisted.Assisted
import dagger.assisted.AssistedFactory
import dagger.assisted.AssistedInject
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.emptyFlow
import kotlinx.coroutines.flow.flatMapLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

/**
 * What one [FeedViewModel] serves. Decided by the destination, not by a
 * setter after the fact, so the first page requested is the right one.
 */
sealed interface FeedMode {
    /** The Home tab: For You / Following under the header, HashTag alongside. */
    data object Home : FeedMode

    /** The Friends tab: the home timeline narrowed to mutual follows. */
    data object Friends : FeedMode

    /** One tag's posts, pushed from the HashTag list. */
    data class Hashtag(val tag: String) : FeedMode
}

/** The HashTag tab's list, as the screen renders it. */
sealed interface TrendingState {
    data object Loading : TrendingState
    data class Content(val tags: List<TrendingHashtag>) : TrendingState
    data class Error(val error: AppError) : TrendingState
}

@HiltViewModel(assistedFactory = FeedViewModel.Factory::class)
// Constructor injection of the timeline's collaborators; a wrapper would add
// indirection, not clarity.
@Suppress("LongParameterList")
class FeedViewModel @AssistedInject constructor(
    @Assisted private val mode: FeedMode,
    private val repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
    private val engagement: EngagementStore,
    private val shares: EngagementRepository,
    private val tabState: FeedTabState,
    private val follows: FollowGraph,
    settings: SettingsDataStore? = null,
) : ViewModel() {

    @AssistedFactory
    interface Factory {
        fun create(mode: FeedMode): FeedViewModel
    }

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
    fun posterUrl(item: FeedItem): String? = item.media.firstOrNull()?.let(::resolveUrl)

    /**
     * Every page of the row's carousel, in the author's order.
     *
     * Built here rather than in the card because URL resolution needs the
     * variant ladder and the viewer's signed delivery, and because the ORDER is
     * a product promise: `item.media` was already validated and sorted by
     * `CarouselOrdinals` on the way out of the paging source, so mapping it
     * index-for-index is what carries that promise to the screen.
     */
    fun mediaPages(item: FeedItem): List<PostCardMediaPage> = item.media.map { media ->
        PostCardMediaPage(
            mediaId = media.mediaId,
            url = resolveUrl(media),
            aspectRatio = media.aspectRatio(),
            // Each page's own description — the same photo cropped two ways can
            // honestly need two different ones.
            contentDescription = media.contentDescription,
        )
    }

    private fun resolveUrl(media: FeedMedia): String? =
        if (media.kind == VIDEO_KIND) {
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

    /** The home header's tab. Meaningful only in [FeedMode.Home]. */
    val tab: StateFlow<FeedTab> = tabState.selected

    fun selectTab(tab: FeedTab) = tabState.select(tab)

    /**
     * `cachedIn(viewModelScope)` is not optional here.
     *
     * Without it the paging flow is re-collected on every configuration change
     * and every recomposition that resubscribes, which refetches page one and
     * throws away the user's scroll position. With it the loaded pages survive
     * rotation.
     *
     * Home holds one cached stream PER TAB rather than rebuilding on switch:
     * `cachedIn` is lazy, so Following costs nothing until it is first shown,
     * and after that flipping back to For You replays its pages instead of
     * refetching them and dropping the reader to the top.
     */
    @OptIn(ExperimentalCoroutinesApi::class)
    val items: Flow<PagingData<FeedItem>> = when (mode) {
        FeedMode.Home -> {
            val pages = mapOf(
                FeedTab.FOR_YOU to cached(FeedQuery.ForYou),
                FeedTab.FOLLOWING to cached(FeedQuery.Following),
            )
            // The HashTag tab has no timeline: the screen shows the trending
            // list instead, and this stream simply goes quiet.
            tab.flatMapLatest { pages[it] ?: emptyFlow() }
        }
        FeedMode.Friends -> cached(FeedQuery.Friends)
        is FeedMode.Hashtag -> repository.hashtagPosts(mode.tag).cachedIn(viewModelScope)
    }.let { flow ->
        if (settings != null) {
            flow.combine(settings.keywordFilters) { page, keywords ->
                if (keywords.isEmpty()) page else page.filter { !KeywordFilter.hides(it, keywords) }
            }
        } else {
            flow
        }
    }

    private fun cached(query: FeedQuery) = repository.feed(query).cachedIn(viewModelScope)

    private val _trending = MutableStateFlow<TrendingState>(TrendingState.Loading)

    /** The HashTag tab's rows. Fetched the first time the tab is shown, not at launch. */
    val trending: StateFlow<TrendingState> = _trending.asStateFlow()

    private var trendingRequested = false

    init {
        if (mode == FeedMode.Home) {
            viewModelScope.launch {
                tab.collect { selected ->
                    if (selected == FeedTab.HASHTAG && !trendingRequested) refreshTrending()
                }
            }
        }
    }

    fun refreshTrending() {
        trendingRequested = true
        _trending.value = TrendingState.Loading
        viewModelScope.launch {
            _trending.value = when (val result = repository.trendingHashtags()) {
                is AppResult.Success -> TrendingState.Content(result.data)
                is AppResult.Failure -> TrendingState.Error(result.error)
            }
        }
    }

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

    /**
     * The viewer's poll votes cast THIS session, postId → optionIds.
     *
     * The same overlay idea as engagement: a PagingData row cannot be edited
     * in place, so the tap is layered over the server's hydration until the
     * next refresh carries it back as `viewer_votes`.
     */
    private val _pollVotes = MutableStateFlow<Map<String, Set<String>>>(emptyMap())
    val pollVotes: StateFlow<Map<String, Set<String>>> = _pollVotes.asStateFlow()

    fun onVotePoll(postId: String, optionId: String) {
        // Optimistic: flip to results immediately; revert if the server said no.
        _pollVotes.update { votes ->
            votes + (postId to (votes[postId].orEmpty() + optionId))
        }
        viewModelScope.launch {
            if (!repository.votePoll(postId, optionId)) {
                _pollVotes.update { votes ->
                    val remaining = votes[postId].orEmpty() - optionId
                    if (remaining.isEmpty()) votes - postId else votes + (postId to remaining)
                }
            }
        }
    }

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
        learnAuthors(items)
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
        learnAuthors(items)
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

    // ── Follow ──────────────────────────────────────────────────────────

    /**
     * Author id → the viewer's edge, for every author the graph has answered
     * for. The card offers "Follow" only when [offersFollow] says so.
     */
    val followEdges: StateFlow<Map<String, FollowStatus>> = follows.edges

    /** The signed-in user; own posts never offer Follow. */
    val ownUserId: String get() = follows.ownId

    fun onFollow(authorId: String) = viewModelScope.launch { follows.follow(authorId) }

    /** One relationship lookup per author never seen before, off the hydration path. */
    private fun learnAuthors(items: List<FeedItem>) = viewModelScope.launch {
        follows.ensureKnown(items.map { it.author.id })
    }
}

/**
 * Feed rows are at most screen-width, so a 720p rung is already more pixels
 * than a phone can show. Asking for 1080p would triple the bytes for a row
 * that gets scrolled past in under a second.
 */
private const val FEED_IMAGE_MAX_HEIGHT = 720

/** `kind` on a feed media entry; `image` is the other value in use. */
private const val VIDEO_KIND = "video"
