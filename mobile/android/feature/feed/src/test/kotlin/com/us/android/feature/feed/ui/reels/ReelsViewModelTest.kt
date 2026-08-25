package com.us.android.feature.feed.ui.reels

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.feed.data.FeedApi
import com.us.android.feature.feed.data.FeedRepository
import com.us.android.feature.feed.data.dto.FeedDeltaDto
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * The URL selection rules, which are where reels breaks if it breaks.
 *
 * `hls_url` arrives gateway-RELATIVE and authorized; the `variants` values are
 * absolute pre-signed object-store URLs. Handing the player the wrong one, or
 * handing it anything at all for an asset that is still processing, produces a
 * playback error where a poster or a spinner belongs.
 */
class ReelsViewModelTest {

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    private object UnusedApi : FeedApi {
        override suspend fun getFeed(surface: String, limit: Int, cursor: String?) =
            error("the reels view model must not fetch in these tests")

        override suspend fun getDelta(
            feedType: String,
            anchor: String,
            limit: Int,
        ): ApiEnvelope<FeedDeltaDto> = error("unused")

        override suspend fun votePoll(
            postId: String,
            body: com.us.android.feature.feed.data.PollVoteRequest,
        ): Nothing = error("unused")
    }

    private fun viewModel() = ReelsViewModel(
        repository = FeedRepository(UnusedApi, ErrorMapper(Json { ignoreUnknownKeys = true })),
        urlResolver = resolver,
    )

    private fun item(vararg media: FeedMedia) = FeedItem(
        id = "p",
        authorId = "a",
        author = FeedAuthor(id = "a", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = media.toList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = true,
    )

    private fun video(
        status: String = "ready",
        hls: String? = "/v1/media/m/hls/master.m3u8",
        variants: Map<String, String> = emptyMap(),
    ) = FeedMedia(
        mediaId = "m",
        kind = "video",
        status = status,
        variants = variants,
        hlsUrl = hls,
    )

    @Test
    fun `a ready video resolves to an absolute gateway url`() {
        val url = viewModel().playbackUrl(item(video()))

        assertThat(url).isEqualTo("http://127.0.0.1:8080/v1/media/m/hls/master.m3u8")
    }

    /**
     * A still-processing asset has no rendition. Returning null lets the pager
     * show "still processing" instead of handing the player a URL that 404s.
     */
    @Test
    fun `an unready asset yields no playback url`() {
        assertThat(viewModel().playbackUrl(item(video(status = "processing")))).isNull()
    }

    @Test
    fun `a ready asset with no hls url yields null rather than a guess`() {
        assertThat(viewModel().playbackUrl(item(video(hls = null)))).isNull()
    }

    @Test
    fun `a post with no media yields null`() {
        assertThat(viewModel().playbackUrl(item())).isNull()
    }

    /** An image attachment is not playable and must not be handed to a player. */
    @Test
    fun `an image attachment is ignored`() {
        val image = FeedMedia(mediaId = "m", kind = "image", status = "ready")

        assertThat(viewModel().playbackUrl(item(image))).isNull()
    }

    @Test
    fun `the poster comes from the thumbnail variant`() {
        val media = video(variants = mapOf("thumb_150" to "http://o/thumb", "360p" to "http://o/360"))

        assertThat(viewModel().posterUrl(item(media))).isEqualTo("http://o/thumb")
    }

    @Test
    fun `no thumbnail variant yields no poster`() {
        assertThat(viewModel().posterUrl(item(video()))).isNull()
    }

    /**
     * Mute is held in the ViewModel, not per player. A per-player flag resets
     * the moment the pool recycles that instance, so the sound would silently
     * come back on after four swipes.
     */
    @Test
    fun `reels start muted and the choice survives toggling`() {
        val vm = viewModel()
        assertThat(vm.muted.value).isTrue()

        vm.toggleMuted()
        assertThat(vm.muted.value).isFalse()

        vm.toggleMuted()
        assertThat(vm.muted.value).isTrue()
    }
}
