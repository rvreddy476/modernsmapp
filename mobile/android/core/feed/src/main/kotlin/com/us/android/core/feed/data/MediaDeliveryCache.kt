package com.us.android.core.feed.data

import com.us.android.core.media.data.MediaDeliveryDto
import java.util.concurrent.ConcurrentHashMap
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The deliveries the hydrator has already fetched, by media id, for as long
 * as their signed URLs are good.
 *
 * One post appears on several Tube surfaces at once — the home shelf, the
 * You page's grid, the Watch "Up next" list — and each hydrates its own rows.
 * Without this every surface would ask `GET /v1/media/{id}/url` for the same
 * still; with it the second and third read the first's answer. The entry
 * outlives the [MediaUrlResolver][com.us.android.core.media.MediaUrlResolver]
 * contract's five-minute signature by a margin of one minute, so a card never
 * paints a URL that has just expired.
 *
 * A delivery with nothing to draw — still processing, no variants — is not
 * kept: the next hydration must ask again, or the still would never arrive.
 */
@Singleton
class MediaDeliveryCache @Inject constructor() {

    private val entries = ConcurrentHashMap<String, Entry>()

    fun get(mediaId: String, now: Long = System.currentTimeMillis()): MediaDeliveryDto? =
        entries[mediaId]?.takeIf { it.freshUntil > now }?.delivery

    fun put(mediaId: String, delivery: MediaDeliveryDto, now: Long = System.currentTimeMillis()) {
        if (delivery.variants.isEmpty()) return
        entries[mediaId] = Entry(delivery, freshUntil = now + TTL_MS)
    }

    private data class Entry(val delivery: MediaDeliveryDto, val freshUntil: Long)

    private companion object {
        /** Signed variant URLs live five minutes; a minute's margin keeps a stale one off a card. */
        const val TTL_MS = 4L * 60L * 1000L
    }
}
