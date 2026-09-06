package com.us.android.feature.post.createhub.studio

import com.us.android.core.media.publish.REEL_MAX_DURATION_MS

/** The Speed step's four chips. */
@Suppress("MagicNumber") // The chips ARE the numbers.
enum class ReelSpeed(val factor: Float, val label: String) {
    HALF(0.5f, "0.5×"),
    NORMAL(1f, "1×"),
    FASTER(1.5f, "1.5×"),
    DOUBLE(2f, "2×"),
}

/** Where the one-line text sits and how it looks. */
enum class TextPillStyle(val label: String) {
    /** White pill, navy text. */
    WHITE("White"),

    /** Navy pill, white text. */
    NAVY("Navy"),
}

/**
 * The text overlay: one line, in a pill, at ([x], [y]) as fractions of
 * the 9:16 frame from its top-left. Draggable on the preview.
 */
data class TextPill(
    val text: String,
    val style: TextPillStyle = TextPillStyle.WHITE,
    val x: Float = CENTRE,
    val y: Float = LOWER_THIRD,
) {
    companion object {
        const val CENTRE = 0.5f

        /** Where a caption sits by default: low, clear of a face in the middle. */
        const val LOWER_THIRD = 0.72f
    }
}

/**
 * Everything the studio changes about a source video (2026-09-05): how
 * it is framed, where it is trimmed, how fast it plays, which look it
 * wears and what one line of text sits over it. The preview reads it
 * live; the export renders it once.
 *
 * Pure. The rules the tests own:
 *  - the trim keeps at least [MIN_TRIM_US] and never runs past the source;
 *  - what is EXPORTED is the trimmed span divided by the speed, and that is
 *    what the five-minute reel cap applies to — a ten-minute clip at 2×
 *    is a legal five-minute reel;
 *  - a handle dragged over the other pushes the other, rather than crossing it.
 */
data class ReelEdit(
    val sourceUri: String,
    /** The source as displayed — rotation applied. */
    val width: Int,
    val height: Int,
    val durationUs: Long,
    val mode: FrameMode = ReelFrame.defaultMode(width, height),
    /** -1..1 along the free axis, Fill only. */
    val pan: Float = 0f,
    val trimStartUs: Long = 0L,
    val trimEndUs: Long = durationUs,
    val speed: ReelSpeed = ReelSpeed.NORMAL,
    val look: ReelLook = ReelLook.NONE,
    val text: TextPill? = null,
) {
    /** The span that is kept, in source time. */
    val trimmedUs: Long
        get() = (trimEndUs - trimStartUs).coerceAtLeast(0L)

    /** How long the exported reel runs: the kept span at the chosen speed. */
    val exportedUs: Long
        get() = (trimmedUs / speed.factor.toDouble()).toLong()

    /** Whether the export would breach the reel cap (the server's five minutes). */
    val exceedsReelCap: Boolean
        get() = exportedUs > REEL_MAX_DURATION_MS * MICROS_PER_MILLI

    /** Whether anything at all differs from the source as picked. */
    val isUntouched: Boolean
        get() = trimStartUs == 0L && trimEndUs == durationUs && speed == ReelSpeed.NORMAL &&
            look == ReelLook.NONE && text == null && pan == 0f && mode == ReelFrame.defaultMode(width, height)

    /** The start handle moved: clamped to the source and held [MIN_TRIM_US] short of the end handle. */
    fun withTrimStart(timeUs: Long): ReelEdit {
        val start = timeUs.coerceIn(0L, (durationUs - MIN_TRIM_US).coerceAtLeast(0L))
        val end = maxOf(trimEndUs, minOf(durationUs, start + MIN_TRIM_US))
        return copy(trimStartUs = start, trimEndUs = end)
    }

    /** The end handle moved: clamped to the source and held [MIN_TRIM_US] past the start handle. */
    fun withTrimEnd(timeUs: Long): ReelEdit {
        val end = timeUs.coerceIn(minOf(MIN_TRIM_US, durationUs), durationUs)
        val start = minOf(trimStartUs, (end - MIN_TRIM_US).coerceAtLeast(0L))
        return copy(trimStartUs = start, trimEndUs = end)
    }

    /** Fill/Fit; the pan is reset when the mode changes, since it only means something in Fill. */
    fun withMode(next: FrameMode): ReelEdit = if (next == mode) this else copy(mode = next, pan = 0f)

    /** A finger dragged the preview by [dragPx] across a preview [previewPx] wide (or tall). */
    fun panned(dragPx: Float, previewPx: Float): ReelEdit =
        if (mode != FrameMode.FILL) this else copy(pan = ReelFrame.panAfterDrag(pan, dragPx, previewPx, width, height))

    companion object {
        /** A reel is at least a second. */
        const val MIN_TRIM_US: Long = 1_000_000L

        private const val MICROS_PER_MILLI = 1_000L
    }
}
