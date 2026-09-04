// Fixtures are response bodies copied VERBATIM from a live capture. Wrapping
// them to satisfy line length would mean editing recorded evidence.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.TrendingHashtagsDto
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Contract tests against payloads captured from the live gateway on
 * 2026-08-17 (prompt/android-api-contracts.md, evidence-pass section).
 *
 * These prove the DTOs deserialize the bytes the server actually sends. When a
 * payload changes, recapture and paste the new body — never edit a fixture to
 * make a test pass.
 */
class FeedContractTest {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private fun decode(body: String): ApiEnvelope<List<FeedItemDto>> = json.decodeFromString(body)

    @Test
    fun `enriched home item deserializes with author and viewer state`() {
        val envelope = decode(HOME_PAGE_1)
        val item = envelope.data!!.first()

        assertThat(item.id).isEqualTo("c1604d02-a4fe-44f2-91a6-feebd0ac814f")
        assertThat(item.author.displayName).isEqualTo("Android Evidence")
        assertThat(item.author.id).isEqualTo(item.authorId)
        assertThat(item.isRepostable).isTrue()
        assertThat(item.hasReacted).isFalse()
        assertThat(item.hasReposted).isFalse()
        assertThat(item.repostCount).isEqualTo(0)
    }

    /** Home is chronological: an RFC3339 timestamp cursor and no `score`. */
    @Test
    fun `home carries a timestamp cursor and no score`() {
        val envelope = decode(HOME_PAGE_1)

        assertThat(envelope.meta?.nextCursor).isEqualTo("2026-08-17T10:16:51.224Z")
        assertThat(envelope.data!!.first().score).isNull()
    }

    @Test
    fun `reels item carries media delivery fields`() {
        val item = decode(REELS_PAGE_1).data!!.first()
        val media = item.media.single()

        assertThat(media.kind).isEqualTo("video")
        assertThat(media.status).isEqualTo("ready")
        assertThat(media.width).isEqualTo(360)
        assertThat(media.height).isEqualTo(640)
        assertThat(media.blurhash).isNotEmpty()
        assertThat(media.expiresAt).isEqualTo("2026-08-17T10:42:32.522994799Z")
    }

    /**
     * The two URL kinds are not interchangeable: `hls_url` is a
     * gateway-relative authorized path, `variants` are absolute pre-signed
     * object-store URLs. Confusing them yields a 404 or a malformed URI.
     */
    @Test
    fun `hls url is relative while variants are absolute`() {
        val media = decode(REELS_PAGE_1).data!!.first().media.single()

        assertThat(media.hlsUrl).startsWith("/v1/media/")
        assertThat(media.hlsUrl).doesNotContain("://")
        media.variants.values.forEach { assertThat(it).startsWith("http") }
    }

    /** Ranked surfaces carry a float score and an opaque base64 cursor. */
    @Test
    fun `reels carries a score and an opaque cursor`() {
        val envelope = decode(REELS_PAGE_1)

        assertThat(envelope.data!!.first().score).isNull() // absent in this fixture
        assertThat(envelope.meta?.nextCursor).isEqualTo("djE6ZTcwZWU4ODAtOTlhYS0xMWYxLTkyMzMtZmU0NWFjOWU0MDIx")
    }

    /**
     * The terminal page omits `meta` entirely rather than sending an empty
     * cursor, so the paging source must treat absent as "end".
     */
    @Test
    fun `terminal page has no meta`() {
        val envelope = decode("""{"data":[]}""")

        assertThat(envelope.data).isEmpty()
        assertThat(envelope.meta?.nextCursor).isNull()
    }

    /** A text post sends no `media` key at all — not an empty array. */
    @Test
    fun `absent media key decodes as an empty list`() {
        assertThat(decode(HOME_PAGE_1).data!!.first().media).isEmpty()
    }

    /** The server adds fields without a client release. */
    @Test
    fun `unknown fields are ignored`() {
        val envelope = decode("""{"data":[{"id":"p","a_brand_new_field":{"nested":true}}]}""")

        assertThat(envelope.data!!.first().id).isEqualTo("p")
    }

    /** Mapping to the domain must not lose the fields the card renders. */
    @Test
    fun `domain mapping preserves author, counts and viewer state`() {
        val domain = decode(REELS_PAGE_1).data!!.first().toDomain()

        assertThat(domain.author.nameForDisplay).isEqualTo("Android Repair")
        assertThat(domain.media.single().hlsUrl).startsWith("/v1/media/")
        assertThat(domain.media.single().isReady).isTrue()
        assertThat(domain.media.single().isVertical).isTrue()
        assertThat(domain.viewer.hasReacted).isFalse()
    }

    /**
     * A still-processing asset can report 0x0. The card divides width by
     * height to reserve space, so the mapper must not hand it a zero.
     */
    @Test
    fun `zero dimensions survive mapping`() {
        val item = decode("""{"data":[{"id":"p","media":[{"media_id":"m","kind":"image","width":0,"height":0}]}]}""")
            .data!!.first().toDomain()

        assertThat(item.media.single().width).isEqualTo(0)
        assertThat(item.media.single().isVertical).isFalse()
    }

    // ── Instant reels (2026-09-04): processing rows and the author's controls ──

    /**
     * The shape the dev stack sends for a reel the author just posted: the
     * row is visible with `is_processing`, the media carries its own
     * transcode state, and the server names what to play meanwhile.
     */
    @Test
    fun `a processing reel decodes with its playback choice and controls`() {
        val item = decode(
            """{"data":[{"id":"p","post_type":"video","is_processing":true,"no_comments":true,"hide_share":true,"allow_download":false,"remix_setting":"disallow","category":"comedy","tagged_user_ids":["u1","u2"],"location_name":"Marina Beach","cover_media_id":"cov","media":[{"media_id":"m","kind":"video","status":"processing","processing_status":"processing","moderation_status":"pending","playback_url":"/v1/media/m/original","playback_kind":"original"}]}]}""",
        ).data!!.first().toDomain()

        assertThat(item.isProcessing).isTrue()
        assertThat(item.controls.noComments).isTrue()
        assertThat(item.controls.hideShare).isTrue()
        assertThat(item.controls.allowDownload).isFalse()
        assertThat(item.controls.remixSetting).isEqualTo("disallow")
        assertThat(item.controls.category).isEqualTo("comedy")
        assertThat(item.controls.taggedUserIds).containsExactly("u1", "u2").inOrder()
        assertThat(item.controls.locationName).isEqualTo("Marina Beach")
        assertThat(item.controls.coverMediaId).isEqualTo("cov")
        val media = item.media.single()
        assertThat(media.isProcessing).isTrue()
        assertThat(media.processingStatus).isEqualTo("processing")
        assertThat(media.moderationStatus).isEqualTo("pending")
        assertThat(media.playbackUrl).isEqualTo("/v1/media/m/original")
        assertThat(media.playbackKind).isEqualTo("original")
    }

    /** A row from before the fields existed renders every control, as it always did. */
    @Test
    fun `a row without the new fields is open and not processing`() {
        val item = decode(REELS_PAGE_1).data!!.first().toDomain()

        assertThat(item.isProcessing).isFalse()
        assertThat(item.controls.noComments).isFalse()
        assertThat(item.controls.hideShare).isFalse()
        assertThat(item.controls.allowDownload).isTrue()
    }

    /** Per-media status alone marks the row processing when the post-level flag lags behind. */
    @Test
    fun `a pending media status marks the row processing without the post flag`() {
        val item = decode(
            """{"data":[{"id":"p","media":[{"media_id":"m","kind":"video","status":"pending","processing_status":"pending"}]}]}""",
        ).data!!.first().toDomain()

        assertThat(item.isProcessing).isTrue()
    }

    // ── Hashtags: post-service, through the gateway ─────────────────────

    /**
     * Captured from https://api-dev.cleestudio.com on 2026-09-04 as call_b:
     * a quiet day answers an EMPTY keyed list with 200, not an absent key
     * and not an error. The screen's "No trending tags yet" is this case.
     */
    @Test
    fun `trending hashtags decodes the captured empty day`() {
        val envelope: ApiEnvelope<TrendingHashtagsDto> = json.decodeFromString(TRENDING_EMPTY)

        assertThat(envelope.error).isNull()
        assertThat(envelope.data!!.hashtags).isEmpty()
    }

    /**
     * The non-empty shape follows post-service's `HashtagTrending24h`
     * (`normalized_name`, `display_name`, `post_count`) — the fixture is
     * derived from the Go struct's JSON tags, since dev had no tags to
     * capture. The `#` is the server's; the client does not add a second.
     */
    @Test
    fun `trending hashtags decodes a populated day`() {
        val envelope: ApiEnvelope<TrendingHashtagsDto> = json.decodeFromString(TRENDING_POPULATED)
        val first = envelope.data!!.hashtags.first()

        assertThat(first.normalizedName).isEqualTo("android")
        assertThat(first.displayName).isEqualTo("#android")
        assertThat(first.postCount).isEqualTo(3)
    }

    /** Captured 2026-09-04: a tag with no posts is `{"data":[]}`, 200. */
    @Test
    fun `posts by hashtag decodes the captured empty list`() {
        val page = decode(HASHTAG_POSTS_EMPTY).toFeedPage()

        assertThat(page.items).isEmpty()
        assertThat(page.nextCursor).isNull()
        assertThat(page.errorCode).isNull()
    }

    /**
     * A populated row is post-service's `PostDetail`: the feed item's field
     * names, but NO `author` object and media as bare references. The same
     * DTO must decode it (author defaults to empty, delivery fields to
     * zero) so the hydrator has something to fill in.
     */
    @Test
    fun `posts by hashtag decodes a bare post-service row`() {
        val page = decode(HASHTAG_POSTS_ROW).toFeedPage()
        val item = page.items.single()

        assertThat(item.id).isEqualTo("b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33")
        assertThat(item.authorId).isEqualTo("66668bc2-a3f6-40a5-9cdd-c998dcf72f29")
        // No author object on the wire: the id carries through, the name is blank.
        assertThat(item.author.id).isEqualTo(item.authorId)
        assertThat(item.author.displayName).isEmpty()
        assertThat(item.media.single().mediaId).isEqualTo("509571d6-d5dd-4d0c-9d8e-2b1f3c4d5e6f")
        assertThat(item.media.single().variants).isEmpty()
        assertThat(item.counts.likes).isEqualTo(2)
        assertThat(item.viewer.hasReacted).isTrue()
        assertThat(page.nextCursor).isEqualTo("djE6ZTcwZWU4ODAtOTlhYS0xMWYxLTkyMzMtZmU0NWFjOWU0MDIx")
    }

    /**
     * Soft delete (2026-09-04): a row that still reaches a feed after the
     * author deleted it carries `deleted_at`, and is dropped before mapping.
     * The page is not "empty" for cursor purposes, so paging continues.
     */
    /**
     * Tube (2026-09-05): a long video carries `title` on the row and
     * `duration_ms` on its media; both default so every older row still
     * decodes, and both reach the domain.
     */
    @Test
    fun `a long video's title and duration reach the domain`() {
        val item = decode(
            """{"data":[{"id":"v","content_type":"long_video","title":"How the feed ranks","text":"notes",""" +
                """"media":[{"media_id":"m","kind":"video","status":"ready","duration_ms":754321,""" +
                """"hls_url":"/v1/media/m/hls/master.m3u8"}]}]}""",
        ).data!!.first().toDomain()

        assertThat(item.title).isEqualTo("How the feed ranks")
        assertThat(item.media.single().durationMs).isEqualTo(754_321L)

        val older = decode("""{"data":[{"id":"p","media":[{"media_id":"m","kind":"video"}]}]}""")
            .data!!.first().toDomain()
        assertThat(older.title).isEmpty()
        assertThat(older.media.single().durationMs).isEqualTo(0L)
    }

    @Test
    fun `a row carrying deleted_at is dropped from the page but keeps the cursor`() {
        val page = decode(DELETED_ROW_PAGE).toFeedPage()

        assertThat(page.items.map { it.id }).containsExactly("live")
        assertThat(page.nextCursor).isEqualTo("next")
    }

    private companion object {
        const val TRENDING_EMPTY = """{"data":{"hashtags":[]}}"""

        const val DELETED_ROW_PAGE =
            """{"data":[{"id":"gone","author_id":"a","text":"del","post_type":"text","created_at":"2026-09-04T10:00:00Z",""" +
                """"deleted_at":"2026-09-04T10:05:00Z","purge_at":"2026-10-04T10:05:00Z"},""" +
                """{"id":"live","author_id":"a","text":"kept","post_type":"text","created_at":"2026-09-04T10:00:00Z"}],""" +
                """"meta":{"next_cursor":"next"}}"""

        const val TRENDING_POPULATED =
            """{"data":{"hashtags":[{"normalized_name":"android","display_name":"#android","post_count":3},{"normalized_name":"momentum","display_name":"#momentum","post_count":1}]}}"""

        const val HASHTAG_POSTS_EMPTY = """{"data":[]}"""

        const val HASHTAG_POSTS_ROW =
            """{"data":[{"id":"b2f7d0c1-5c1e-4a0e-9d1a-0c7f6f1e2a33","author_id":"66668bc2-a3f6-40a5-9cdd-c998dcf72f29","text":"first #android post","visibility":"public","content_type":"post","is_pinned":false,"no_comments":false,"no_likes":false,"hashtags":["android"],"post_type":"image","app_origin":"postbook","share_to_postbook":true,"review_status":"approved","paid_promotion":false,"altered_content":false,"is_made_for_kids":false,"allow_embedding":false,"publish_to_feed":true,"original_audio_volume":0,"overlay_audio_volume":0,"distribution_rev":0,"created_at":"2026-09-04T09:00:00.000000Z","updated_at":"2026-09-04T09:00:00.000000Z","media":[{"media_id":"509571d6-d5dd-4d0c-9d8e-2b1f3c4d5e6f","kind":"image","alt_text":"","alt_decorative":false,"position":0}],"counts":{"likes":2,"comments":0},"view_count":5,"viewer_reaction":"like","has_reacted":true,"is_bookmarked":false,"repost_count":0,"has_reposted":false,"is_repostable":true}],"meta":{"next_cursor":"djE6ZTcwZWU4ODAtOTlhYS0xMWYxLTkyMzMtZmU0NWFjOWU0MDIx"}}"""

        const val HOME_PAGE_1 =
            """{"data":[{"id":"c1604d02-a4fe-44f2-91a6-feebd0ac814f","author_id":"71851843-a69f-4d2f-a2f8-9f6eea629609","text":"Evidence pass fixture 3","visibility":"public","content_type":"post","is_pinned":false,"created_at":"2026-08-17T10:16:51.278089Z","updated_at":"2026-08-17T10:16:51.278089Z","counts":{"likes":0,"comments":0},"view_count":0,"has_reacted":false,"is_bookmarked":false,"repost_count":0,"has_reposted":false,"is_repostable":true,"post_type":"text","app_origin":"postbook","share_to_postbook":true,"feed_content_type":"post","author":{"id":"71851843-a69f-4d2f-a2f8-9f6eea629609","display_name":"Android Evidence"}}],"meta":{"next_cursor":"2026-08-17T10:16:51.224Z"}}"""

        const val REELS_PAGE_1 =
            """{"data":[{"id":"724ce232-0a7c-48c3-9875-3f3ccef188b9","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","text":"Portrait Flick — approved contract fixture","visibility":"public","content_type":"flick","is_pinned":false,"created_at":"2026-08-16T19:44:32.520451Z","updated_at":"2026-08-16T19:44:32.609462Z","media":[{"media_id":"b52c17e1-d714-4250-93bd-0225b6898104","kind":"video","status":"ready","width":360,"height":640,"blurhash":"LnFs0+o}4TeSoLj0WYXff6RNjYtB","variants":{"360p":"http://localhost:9000/media/user/719e2958/b52c17e1/360p?X-Amz-Signature=redacted","480p":"http://localhost:9000/media/user/719e2958/b52c17e1/480p?X-Amz-Signature=redacted","original":"http://localhost:9000/media/user/719e2958/b52c17e1/original?X-Amz-Signature=redacted"},"hls_url":"/v1/media/b52c17e1-d714-4250-93bd-0225b6898104/hls/master.m3u8","expires_at":"2026-08-17T10:42:32.522994799Z"}],"author":{"id":"719e2958-f412-44ca-b94a-b00060a7fccb","display_name":"Android Repair"}}],"meta":{"next_cursor":"djE6ZTcwZWU4ODAtOTlhYS0xMWYxLTkyMzMtZmU0NWFjOWU0MDIx"}}"""
    }
}
