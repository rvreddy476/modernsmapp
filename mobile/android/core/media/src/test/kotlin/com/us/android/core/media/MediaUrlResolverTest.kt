package com.us.android.core.media

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiConfig
import org.junit.Test

/**
 * The captured media contract has two URL kinds that look similar and behave
 * nothing alike:
 *
 *  - `hls_url` is gateway-RELATIVE (`/v1/media/:id/hls/master.m3u8`).
 *  - `variants` values are ABSOLUTE, pre-signed object-store URLs.
 *
 * Prefixing a signed variant with the gateway produces a 404; opening the
 * relative HLS path directly produces a malformed URI. Every test below pins
 * one side of that distinction.
 */
class MediaUrlResolverTest {

    private val resolver = MediaUrlResolver(
        ApiConfig(
            baseUrl = "http://127.0.0.1:8080",
            wsBaseUrl = "ws://127.0.0.1:8093",
            clientVersion = "test",
            environment = "test",
            isDebug = true,
        ),
    )

    @Test
    fun `the captured relative hls path resolves against the gateway`() {
        val captured = "/v1/media/170ba790-3a6e-4899-ab43-c1e6db3ea2c4/hls/master.m3u8"

        assertThat(resolver.hlsUrl(captured))
            .isEqualTo("http://127.0.0.1:8080/v1/media/170ba790-3a6e-4899-ab43-c1e6db3ea2c4/hls/master.m3u8")
    }

    /**
     * Production is expected to return a CDN URL here instead of a gateway
     * path. That must keep working without a client release, so an absolute
     * value passes through untouched.
     */
    @Test
    fun `an absolute hls url passes through unchanged`() {
        val cdn = "https://cdn.example.com/media/abc/hls/master.m3u8"

        assertThat(resolver.hlsUrl(cdn)).isEqualTo(cdn)
    }

    @Test
    fun `a missing hls url yields null rather than a guessed path`() {
        assertThat(resolver.hlsUrl(null)).isNull()
        assertThat(resolver.hlsUrl("")).isNull()
        assertThat(resolver.hlsUrl("   ")).isNull()
    }

    /** A double slash is a different path to most servers. */
    @Test
    fun `joining never doubles the slash`() {
        val trailing = MediaUrlResolver(
            ApiConfig("http://127.0.0.1:8080/", "ws://x", "t", "t", true),
        )

        assertThat(trailing.hlsUrl("/v1/media/a/hls/master.m3u8"))
            .isEqualTo("http://127.0.0.1:8080/v1/media/a/hls/master.m3u8")
    }

    @Test
    fun `the tallest variant within the ceiling wins`() {
        val variants = mapOf(
            "360p" to "u360",
            "720p" to "u720",
            "1080p" to "u1080",
        )

        assertThat(resolver.bestVariant(variants, maxHeight = 720)).isEqualTo("u720")
        assertThat(resolver.bestVariant(variants, maxHeight = 1080)).isEqualTo("u1080")
        assertThat(resolver.bestVariant(variants, maxHeight = 480)).isEqualTo("u360")
    }

    /**
     * `original` and `thumb_150` are in the same map as the ladder. Selecting
     * `original` would hand a phone an arbitrarily large file, and `thumb_150`
     * is a still image, not a rendition.
     */
    @Test
    fun `original and thumbnail are never chosen as a video variant`() {
        val variants = mapOf(
            "original" to "uOriginal",
            "thumb_150" to "uThumb",
            "360p" to "u360",
        )

        assertThat(resolver.bestVariant(variants, maxHeight = 4000)).isEqualTo("u360")
    }

    @Test
    fun `no variant fits below the ceiling`() {
        assertThat(resolver.bestVariant(mapOf("1080p" to "u"), maxHeight = 240)).isNull()
    }

    @Test
    fun `an empty variant map yields null`() {
        assertThat(resolver.bestVariant(emptyMap(), maxHeight = 1080)).isNull()
    }

    @Test
    fun `the thumbnail comes from its own key`() {
        val variants = mapOf("thumb_150" to "uThumb", "360p" to "u360")

        assertThat(resolver.thumbnail(variants)).isEqualTo("uThumb")
        assertThat(resolver.thumbnail(mapOf("360p" to "u360"))).isNull()
    }

    /**
     * The captured signed variant URLs are absolute and point at the object
     * store, not the gateway. Passing one to the HLS resolver by mistake must
     * not silently rewrite it.
     */
    @Test
    fun `a signed variant url survives the hls resolver untouched`() {
        val signed = "http://localhost:9000/media/user/abc/def/360p?X-Amz-Signature=redacted"

        assertThat(resolver.hlsUrl(signed)).isEqualTo(signed)
    }
}
