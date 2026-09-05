package com.us.android.feature.post.createhub

import java.util.Locale

/**
 * The cover picker's timeline math (founder, 2026-09-05: an exact-frame
 * cover picker). Pure, so the strip's spacing, the handle's mapping and
 * the readout are table tests.
 */
object Filmstrip {
    /** Evenly spaced thumbnails across the strip. */
    const val FRAME_COUNT = 24

    /**
     * Where the strip's thumbnails are taken from: [count] instants spread
     * evenly over `[0, durationUs)`, the first at 0 and the last held back
     * from the very end, where many encoders leave a black frame. A video
     * of no known length yields every instant at 0.
     */
    fun timestampsUs(durationUs: Long, count: Int = FRAME_COUNT): List<Long> {
        if (count <= 0) return emptyList()
        val length = durationUs.coerceAtLeast(0L)
        return List(count) { index -> length * index / count }
    }

    /**
     * The instant under the handle at [fraction] of the strip's width —
     * clamped to the strip, and short of the very end by [TAIL_MARGIN_US]
     * so the last frame asked for exists.
     */
    fun timeAt(fraction: Float, durationUs: Long): Long {
        val length = durationUs.coerceAtLeast(0L)
        val last = (length - TAIL_MARGIN_US).coerceAtLeast(0L)
        return (length * fraction.coerceIn(0f, 1f).toDouble()).toLong().coerceIn(0L, last)
    }

    /** The handle's place for an instant: 0..1 of the strip. */
    fun fractionOf(timeUs: Long, durationUs: Long): Float =
        if (durationUs <= 0L) 0f else (timeUs.toDouble() / durationUs).toFloat().coerceIn(0f, 1f)

    /** "0:42.6" — minutes, seconds, tenths; "1:02:03.4" from an hour on. */
    fun format(timeUs: Long): String {
        val tenths = (timeUs.coerceAtLeast(0L) + HALF_TENTH_US) / TENTH_US
        val seconds = tenths / TENTHS_PER_SECOND
        val tenth = tenths % TENTHS_PER_SECOND
        val hours = seconds / SECONDS_PER_HOUR
        val minutes = (seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
        val second = seconds % SECONDS_PER_MINUTE
        return if (hours > 0) {
            String.format(Locale.US, "%d:%02d:%02d.%d", hours, minutes, second, tenth)
        } else {
            String.format(Locale.US, "%d:%02d.%d", minutes, second, tenth)
        }
    }

    /** How far short of the end the handle can go: a tenth of a second. */
    const val TAIL_MARGIN_US = 100_000L

    private const val TENTH_US = 100_000L
    private const val HALF_TENTH_US = 50_000L
    private const val TENTHS_PER_SECOND = 10L
    private const val SECONDS_PER_MINUTE = 60L
    private const val SECONDS_PER_HOUR = 3_600L
}
