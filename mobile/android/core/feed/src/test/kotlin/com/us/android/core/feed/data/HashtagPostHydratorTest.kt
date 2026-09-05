package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.data.MediaApi
import com.us.android.core.media.data.MediaDeliveryDto
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.profile.data.ProfileRepository
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.runBlocking
import org.junit.Test
import java.io.IOException

/**
 * Where a card's still comes from once a row has been through the hydrator:
 * the chosen cover, else the row's own variants, else the media delivery
 * endpoint, else nothing — and the delivery is asked for once per id.
 *
 * The continue-watching blank-card bug (2026-09-05) lived in the second
 * step: post-service's `PostDetail` overlays `hls_url` on a video but never
 * `variants`, and a present `hls_url` used to count as "delivered".
 */
class HashtagPostHydratorTest {

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )
    private val profiles = mockk<ProfileRepository>()
    private val media = mockk<MediaApi>()
    private val hydrator = HashtagPostHydrator(profiles, media, MediaDeliveryCache())

    /** The shape post-service sends for a transcoded video: a playlist and a length, no still. */
    private val hlsOnlyVideo = FeedMedia(
        mediaId = "vid",
        kind = "video",
        hlsUrl = "/v1/media/vid/hls/master.m3u8",
        processingStatus = "ready",
        durationMs = 12_000L,
    )
    private val videoDelivery = MediaDeliveryDto(
        mediaId = "vid",
        kind = "video",
        status = "ready",
        blurhash = "VIDHASH",
        variants = mapOf("thumb_150" to "https://obj/vid/thumb.jpg", "720p" to "https://obj/vid/720.mp4"),
        hlsUrl = "/v1/media/vid/hls/master.m3u8",
    )
    private val coverDelivery = MediaDeliveryDto(
        mediaId = "cov",
        kind = "image",
        status = "ready",
        blurhash = "COVHASH",
        variants = mapOf("thumb_150" to "https://obj/cov/thumb.jpg", "720p" to "https://obj/cov/720.jpg"),
    )

    @Test
    fun `the chosen cover wins, resolved through the delivery endpoint`() = runBlocking {
        coEvery { media.getDelivery("vid") } returns ApiEnvelope(data = videoDelivery)
        coEvery { media.getDelivery("cov") } returns ApiEnvelope(data = coverDelivery)

        val thumb = resolver.videoThumb(hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = "cov"))).single())

        assertThat(thumb.url).isEqualTo("https://obj/cov/720.jpg")
        assertThat(thumb.blurhash).isEqualTo("COVHASH")
    }

    @Test
    fun `a row that already carries variants is not looked up again`() = runBlocking {
        val delivered = hlsOnlyVideo.copy(variants = mapOf("thumb_150" to "https://obj/vid/feed-thumb.jpg"))

        val thumb = resolver.videoThumb(hydrator.hydrate(listOf(item(delivered, coverId = null))).single())

        assertThat(thumb.url).isEqualTo("https://obj/vid/feed-thumb.jpg")
        coVerify(exactly = 0) { media.getDelivery(any()) }
    }

    @Test
    fun `a video with only an HLS path gets its still from the delivery endpoint`() = runBlocking {
        coEvery { media.getDelivery("vid") } returns ApiEnvelope(data = videoDelivery.copy(hlsUrl = null))

        val hydrated = hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = null))).single()
        val thumb = resolver.videoThumb(hydrated)

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
        assertThat(thumb.blurhash).isEqualTo("VIDHASH")
        // What the row already knew survives a delivery that does not repeat it.
        assertThat(hydrated.media.single().hlsUrl).isEqualTo("/v1/media/vid/hls/master.m3u8")
        assertThat(thumb.durationMs).isEqualTo(12_000L)
    }

    @Test
    fun `a delivery with nothing to draw leaves no still and is asked again next time`() = runBlocking {
        coEvery { media.getDelivery("vid") } returns ApiEnvelope(data = videoDelivery.copy(variants = emptyMap()))

        repeat(2) {
            val thumb = resolver.videoThumb(hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = null))).single())
            assertThat(thumb.url).isNull()
        }
        coVerify(exactly = 2) { media.getDelivery("vid") }
    }

    @Test
    fun `a delivery that fails leaves the row playable and without a still`() = runBlocking {
        coEvery { media.getDelivery("vid") } throws IOException("offline")

        val hydrated = hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = null))).single()

        assertThat(resolver.videoThumb(hydrated).url).isNull()
        assertThat(hydrated.media.single().hlsUrl).isEqualTo("/v1/media/vid/hls/master.m3u8")
    }

    @Test
    fun `a delivery is fetched once per media id across hydrations`() = runBlocking {
        coEvery { media.getDelivery("vid") } returns ApiEnvelope(data = videoDelivery)

        val first = hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = null))).single()
        val second = hydrator.hydrate(listOf(item(hlsOnlyVideo, coverId = null), item(hlsOnlyVideo, coverId = null)))

        assertThat(resolver.videoThumb(first).url).isEqualTo("https://obj/vid/thumb.jpg")
        assertThat(second.map { resolver.videoThumb(it).url }).containsExactly(
            "https://obj/vid/thumb.jpg",
            "https://obj/vid/thumb.jpg",
        )
        coVerify(exactly = 1) { media.getDelivery("vid") }
    }

    @Test
    fun `the cache forgets a delivery once its signed URLs may have expired`() {
        val cache = MediaDeliveryCache()
        cache.put("vid", videoDelivery, now = 0L)

        assertThat(cache.get("vid", now = 3 * 60_000L)).isEqualTo(videoDelivery)
        assertThat(cache.get("vid", now = 5 * 60_000L)).isNull()
    }

    private fun item(video: FeedMedia, coverId: String?) = FeedItem(
        id = "p",
        authorId = "u",
        author = FeedAuthor(id = "u", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = listOf(video),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
        controls = FeedPostControls(coverMediaId = coverId),
    )
}
