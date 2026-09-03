package com.us.android.feature.feed.ui.reels

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.paging.PagingData
import androidx.paging.cachedIn
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedQuery
import com.us.android.feature.feed.data.FeedRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject

@HiltViewModel
class ReelsViewModel @Inject constructor(
    repository: FeedRepository,
    private val urlResolver: MediaUrlResolver,
) : ViewModel() {

    /**
     * `cachedIn` is not optional: without it, rotating the device refetches
     * page one and drops the user back to the first reel.
     */
    val items: Flow<PagingData<FeedItem>> =
        repository.feed(FeedQuery.Reels).cachedIn(viewModelScope)

    private val _muted = MutableStateFlow(true)

    /**
     * Reels start muted and stay muted until the viewer says otherwise.
     *
     * Autoplaying sound in a scrolling surface is hostile in public, and every
     * platform that ships it also ships an unmute affordance. The choice is
     * held here rather than per-player so it survives page changes and player
     * recycling — a per-player flag resets the moment the pool reclaims one.
     */
    val muted: StateFlow<Boolean> = _muted.asStateFlow()

    fun toggleMuted() {
        _muted.value = !_muted.value
    }

    /**
     * The absolute, playable HLS URL for an item, or null when there is
     * nothing to play.
     *
     * Null is a real outcome, not a defect: an asset still processing has no
     * `hls_url`, and the server sends the field only once a rendition exists.
     * Returning null lets the pager show a poster rather than handing the
     * player a URL it will fail on.
     *
     * The resolution itself matters — `hls_url` arrives gateway-relative and
     * authorized, so it must be joined to the API base URL and fetched with
     * the bearer token. The signed `variants` URLs are absolute and must never
     * be run through the same path.
     */
    fun playbackUrl(item: FeedItem): String? {
        val media = item.media.firstOrNull { it.kind == VIDEO } ?: return null
        if (!media.isReady) return null
        return urlResolver.hlsUrl(media.hlsUrl)
    }

    /** The still frame to show before the first video frame decodes. */
    fun posterUrl(item: FeedItem): String? =
        item.media.firstOrNull { it.kind == VIDEO }?.let { urlResolver.thumbnail(it.variants) }

    private companion object {
        const val VIDEO = "video"
    }
}
