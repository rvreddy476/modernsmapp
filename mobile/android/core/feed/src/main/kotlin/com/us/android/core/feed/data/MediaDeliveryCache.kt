package com.us.android.core.feed.data

import com.us.android.core.media.data.MediaDeliveryDto
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
 *
 * Bounded, because it is process-wide and every media id a session hydrates
 * passes through it: an expired entry is removed when it is read, and past
 * [maxEntries] the least recently used one goes. A dropped entry costs one
 * more `GET /v1/media/{id}/url`, never a wrong picture.
 */
@Singleton
class MediaDeliveryCache internal constructor(private val maxEntries: Int) {

    @Inject constructor() : this(MAX_ENTRIES)

    /** Access-ordered, so the eldest entry is the least recently used. */
    private val entries = object : LinkedHashMap<String, Entry>(INITIAL_CAPACITY, LOAD_FACTOR, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Entry>): Boolean = size > maxEntries
    }

    fun get(mediaId: String, now: Long = System.currentTimeMillis()): MediaDeliveryDto? = synchronized(entries) {
        val entry = entries[mediaId]
        when {
            entry == null -> null
            entry.freshUntil > now -> entry.delivery
            else -> {
                // Stale: its URLs are about to expire, so it is gone, not kept for later.
                entries.remove(mediaId)
                null
            }
        }
    }

    fun put(mediaId: String, delivery: MediaDeliveryDto, now: Long = System.currentTimeMillis()) {
        if (delivery.variants.isEmpty()) return
        synchronized(entries) { entries[mediaId] = Entry(delivery, freshUntil = now + TTL_MS) }
    }

    /** How many entries are held; for tests. */
    internal val size: Int get() = synchronized(entries) { entries.size }

    private data class Entry(val delivery: MediaDeliveryDto, val freshUntil: Long)

    private companion object {
        /** Signed variant URLs live five minutes; a minute's margin keeps a stale one off a card. */
        const val TTL_MS = 4L * 60L * 1000L

        /** About thirty pages of rows with a cover each: far more than four minutes of scrolling shows. */
        const val MAX_ENTRIES = 500

        const val INITIAL_CAPACITY = 64
        const val LOAD_FACTOR = 0.75f
    }
}
