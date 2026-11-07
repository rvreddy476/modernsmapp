package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.data.MediaDeliveryDto
import org.junit.Test

/**
 * The process-wide delivery cache stays bounded (audit 2026-10-30, A3): an
 * expired entry leaves when it is read, and past the cap the least recently
 * used entry goes, so a long session no longer keeps every media id it ever
 * hydrated.
 */
class MediaDeliveryCacheTest {

    private fun delivery(id: String) = MediaDeliveryDto(mediaId = id, variants = mapOf("720p" to "https://obj/$id.jpg"))

    @Test
    fun `a fresh entry is served, and an expired one is removed when read`() {
        val cache = MediaDeliveryCache()
        cache.put("m1", delivery("m1"), now = 0L)

        assertThat(cache.get("m1", now = TTL - 1)).isEqualTo(delivery("m1"))
        assertThat(cache.get("m1", now = TTL)).isNull()
        assertThat(cache.size).isEqualTo(0)
    }

    @Test
    fun `past the cap the least recently used entry is dropped`() {
        val cache = MediaDeliveryCache(maxEntries = 2)
        cache.put("a", delivery("a"), now = 0L)
        cache.put("b", delivery("b"), now = 0L)
        // Reading "a" makes "b" the least recently used.
        assertThat(cache.get("a", now = 1L)).isNotNull()

        cache.put("c", delivery("c"), now = 1L)

        assertThat(cache.size).isEqualTo(2)
        assertThat(cache.get("b", now = 1L)).isNull()
        assertThat(cache.get("a", now = 1L)).isNotNull()
        assertThat(cache.get("c", now = 1L)).isNotNull()
    }

    @Test
    fun `a delivery with nothing to draw is never kept`() {
        val cache = MediaDeliveryCache()
        cache.put("m1", MediaDeliveryDto(mediaId = "m1"), now = 0L)

        assertThat(cache.get("m1", now = 0L)).isNull()
        assertThat(cache.size).isEqualTo(0)
    }

    @Test
    fun `the production cache holds five hundred entries`() {
        val cache = MediaDeliveryCache()
        repeat(MAX + 1) { cache.put("m$it", delivery("m$it"), now = 0L) }

        assertThat(cache.size).isEqualTo(MAX)
        assertThat(cache.get("m0", now = 0L)).isNull()
    }

    private companion object {
        const val TTL = 4L * 60L * 1000L
        const val MAX = 500
    }
}
