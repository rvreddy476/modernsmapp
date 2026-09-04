package com.us.android.feature.tube.ui.watch

/**
 * The resume rule (Tube, 2026-09-05): a video opens where the viewer left
 * it unless they had all but finished — 95% or more counts as watched, and
 * a watched video starts again from the top. The same threshold marks a
 * report as `completed`, so the two never disagree about what "finished"
 * means.
 *
 * Pure, so it is a table test. An unknown duration cannot say how far along
 * the position is: it resumes as saved and is never "completed".
 */
const val COMPLETION_FRACTION = 0.95

/** Where to seek on open: the saved playhead, or 0 for a finished (or never-started) video. */
fun resumePositionMs(positionMs: Long, durationMs: Long): Long {
    if (positionMs <= 0L) return 0L
    if (isCompleted(positionMs, durationMs)) return 0L
    return positionMs
}

/** Whether a playhead counts as having watched the video. */
fun isCompleted(positionMs: Long, durationMs: Long): Boolean =
    durationMs > 0L && positionMs.toDouble() / durationMs >= COMPLETION_FRACTION
