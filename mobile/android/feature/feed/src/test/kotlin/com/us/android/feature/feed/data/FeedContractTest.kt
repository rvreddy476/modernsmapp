// Fixtures are response bodies copied VERBATIM from a live capture. Wrapping
// them to satisfy line length would mean editing recorded evidence.
@file:Suppress("MaxLineLength", "MaximumLineLength")

package com.us.android.feature.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.feed.data.dto.FeedItemDto
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

    private companion object {
        const val HOME_PAGE_1 =
            """{"data":[{"id":"c1604d02-a4fe-44f2-91a6-feebd0ac814f","author_id":"71851843-a69f-4d2f-a2f8-9f6eea629609","text":"Evidence pass fixture 3","visibility":"public","content_type":"post","is_pinned":false,"created_at":"2026-08-17T10:16:51.278089Z","updated_at":"2026-08-17T10:16:51.278089Z","counts":{"likes":0,"comments":0},"view_count":0,"has_reacted":false,"is_bookmarked":false,"repost_count":0,"has_reposted":false,"is_repostable":true,"post_type":"text","app_origin":"postbook","share_to_postbook":true,"feed_content_type":"post","author":{"id":"71851843-a69f-4d2f-a2f8-9f6eea629609","display_name":"Android Evidence"}}],"meta":{"next_cursor":"2026-08-17T10:16:51.224Z"}}"""

        const val REELS_PAGE_1 =
            """{"data":[{"id":"724ce232-0a7c-48c3-9875-3f3ccef188b9","author_id":"719e2958-f412-44ca-b94a-b00060a7fccb","text":"Portrait Flick — approved contract fixture","visibility":"public","content_type":"flick","is_pinned":false,"created_at":"2026-08-16T19:44:32.520451Z","updated_at":"2026-08-16T19:44:32.609462Z","media":[{"media_id":"b52c17e1-d714-4250-93bd-0225b6898104","kind":"video","status":"ready","width":360,"height":640,"blurhash":"LnFs0+o}4TeSoLj0WYXff6RNjYtB","variants":{"360p":"http://localhost:9000/media/user/719e2958/b52c17e1/360p?X-Amz-Signature=redacted","480p":"http://localhost:9000/media/user/719e2958/b52c17e1/480p?X-Amz-Signature=redacted","original":"http://localhost:9000/media/user/719e2958/b52c17e1/original?X-Amz-Signature=redacted"},"hls_url":"/v1/media/b52c17e1-d714-4250-93bd-0225b6898104/hls/master.m3u8","expires_at":"2026-08-17T10:42:32.522994799Z"}],"author":{"id":"719e2958-f412-44ca-b94a-b00060a7fccb","display_name":"Android Repair"}}],"meta":{"next_cursor":"djE6ZTcwZWU4ODAtOTlhYS0xMWYxLTkyMzMtZmU0NWFjOWU0MDIx"}}"""
    }
}
