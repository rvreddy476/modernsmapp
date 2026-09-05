package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia
import com.us.android.core.model.FeedPostControls
import com.us.android.core.model.FeedViewerState
import com.us.android.core.network.ApiConfig
import org.junit.Test

/** Which still a card shows, and where its wash and length come from — the cover resolution rule. */
class VideoThumbTest {

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    private val video = FeedMedia(
        mediaId = "vid",
        kind = "video",
        blurhash = "VIDEOHASH",
        variants = mapOf("thumb_150" to "https://obj/vid/thumb.jpg", "720p" to "https://obj/vid/720.mp4"),
        durationMs = 754_321L,
    )
    private val cover = FeedMedia(
        mediaId = "cov",
        kind = "image",
        blurhash = "COVERHASH",
        variants = mapOf(
            "thumb_150" to "https://obj/cov/thumb.jpg",
            "360p" to "https://obj/cov/360.jpg",
            "720p" to "https://obj/cov/720.jpg",
            "1080p" to "https://obj/cov/1080.jpg",
        ),
    )

    @Test
    fun `the chosen cover wins at up to 720 tall, with its own wash`() {
        val thumb = resolver.videoThumb(item(listOf(video, cover), coverId = "cov"))

        assertThat(thumb.url).isEqualTo("https://obj/cov/720.jpg")
        assertThat(thumb.blurhash).isEqualTo("COVERHASH")
        assertThat(thumb.durationMs).isEqualTo(754_321L)
    }

    @Test
    fun `without a cover the video's own still is used`() {
        val thumb = resolver.videoThumb(item(listOf(video), coverId = null))

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
        assertThat(thumb.blurhash).isEqualTo("VIDEOHASH")
    }

    @Test
    fun `a cover id the row does not carry falls back to the video`() {
        val thumb = resolver.videoThumb(item(listOf(video), coverId = "missing"))

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
    }

    @Test
    fun `a cover with only a thumbnail still shows it`() {
        val small = cover.copy(variants = mapOf("thumb_150" to "https://obj/cov/thumb.jpg"))
        val thumb = resolver.videoThumb(item(listOf(video, small), coverId = "cov"))

        assertThat(thumb.url).isEqualTo("https://obj/cov/thumb.jpg")
    }

    @Test
    fun `a cover the hydrator resolved wins over the video still`() {
        val thumb = resolver.videoThumb(item(listOf(video), coverId = "cov").copy(coverMedia = cover))

        assertThat(thumb.url).isEqualTo("https://obj/cov/720.jpg")
        assertThat(thumb.blurhash).isEqualTo("COVERHASH")
    }

    @Test
    fun `a resolved cover for another id is ignored`() {
        val stale = item(listOf(video), coverId = "cov").copy(coverMedia = cover.copy(mediaId = "old"))
        val thumb = resolver.videoThumb(stale)

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
    }

    @Test
    fun `a cover id that names the video itself never picks a rendition`() {
        val thumb = resolver.videoThumb(item(listOf(video), coverId = "vid"))

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
    }

    @Test
    fun `a resolved cover without delivery yet falls back to the video`() {
        val bare = cover.copy(variants = emptyMap())
        val thumb = resolver.videoThumb(item(listOf(video), coverId = "cov").copy(coverMedia = bare))

        assertThat(thumb.url).isEqualTo("https://obj/vid/thumb.jpg")
    }

    @Test
    fun `a portrait video is flagged so a grid can give it a taller tile`() {
        val tall = video.copy(width = 720, height = 1280)

        assertThat(resolver.videoThumb(item(listOf(tall), coverId = null)).isPortrait).isTrue()
        assertThat(resolver.videoThumb(item(listOf(video), coverId = null)).isPortrait).isFalse()
    }

    @Test
    fun `the hydrator asks only for a cover the row does not carry`() {
        assertThat(item(listOf(video), coverId = "cov").coverIdToResolve()).isEqualTo("cov")
        assertThat(item(listOf(video, cover), coverId = "cov").coverIdToResolve()).isNull()
        assertThat(item(listOf(video), coverId = "cov").copy(coverMedia = cover).coverIdToResolve()).isNull()
        assertThat(item(listOf(video), coverId = null).coverIdToResolve()).isNull()
    }

    @Test
    fun `a video with only an HLS path has no still until the hydrator fetches its delivery`() {
        val hlsOnly = FeedMedia(
            mediaId = "vid",
            kind = "video",
            hlsUrl = "/v1/media/vid/hls/master.m3u8",
            durationMs = 9_000L,
        )
        val thumb = resolver.videoThumb(item(listOf(hlsOnly), coverId = null))

        assertThat(thumb.url).isNull()
        assertThat(thumb.durationMs).isEqualTo(9_000L)
    }

    @Test
    fun `no media at all is no still and no length`() {
        val thumb = resolver.videoThumb(item(emptyList(), coverId = null))

        assertThat(thumb.url).isNull()
        assertThat(thumb.blurhash).isEmpty()
        assertThat(thumb.durationMs).isEqualTo(0L)
    }

    private fun item(media: List<FeedMedia>, coverId: String?) = FeedItem(
        id = "p",
        authorId = "u",
        author = FeedAuthor(id = "u", displayName = "Ada"),
        text = "",
        visibility = "public",
        feedContentType = "long_video",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = media,
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
        controls = FeedPostControls(coverMediaId = coverId),
    )
}
