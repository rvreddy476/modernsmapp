package com.us.android.core.model

/**
 * One chapter of a long video: where it starts and what it is called.
 *
 * The post detail's `chapters: [{start_ms, title}]` (post-service
 * service/post.go:599) — the creator's saved chapters, or, for a long video
 * with none, the ones the server derives from description timestamps
 * (service/chapters.go:105). Only the direct read carries them; list rows
 * send null, so a video opened from a list learns them from its detail.
 */
data class VideoChapter(val startMs: Long, val title: String)

/**
 * The detail's chapters as the watch page reads them — the web's
 * `normalizeChapters` (features/posttube/watch/chapters.ts): a negative start
 * is dropped, a second chapter at the same millisecond is dropped, a blank
 * title is the chapter's clock, and the list is sorted by start.
 */
fun normalizeChapters(raw: List<VideoChapter>): List<VideoChapter> {
    val seen = mutableSetOf<Long>()
    return raw
        .filter { it.startMs >= 0 && seen.add(it.startMs) }
        .map { it.copy(title = it.title.trim().ifBlank { formatChapterClock(it.startMs) }) }
        .sortedBy { it.startMs }
}

/** The index of the chapter playing at [positionMs]: -1 before the first one, or with none. */
fun chapterIndexAt(chapters: List<VideoChapter>, positionMs: Long): Int {
    var at = -1
    for ((index, chapter) in chapters.withIndex()) {
        if (chapter.startMs <= positionMs) at = index else break
    }
    return at
}

/** The chapter playing at [positionMs], or null before the first one / with none. */
fun chapterAt(chapters: List<VideoChapter>, positionMs: Long): VideoChapter? =
    chapters.getOrNull(chapterIndexAt(chapters, positionMs))

/**
 * Where each chapter starts on the seek bar, as a fraction (0..1) of
 * [durationMs] — the web's `chapterTicks`: none while the length is
 * unknown, none for the chapter at 0 (the bar's own start) or for one at or
 * past the end.
 */
fun chapterTicks(chapters: List<VideoChapter>, durationMs: Long): List<Float> {
    if (durationMs <= 0L) return emptyList()
    return chapters
        .filter { it.startMs in 1 until durationMs }
        .map { (it.startMs.toDouble() / durationMs).toFloat() }
}

/** `m:ss`, or `h:mm:ss` from an hour — how a chapter's start is printed. */
fun formatChapterClock(ms: Long): String {
    val total = (ms / MILLIS_PER_SECOND).coerceAtLeast(0L)
    val hours = total / SECONDS_PER_HOUR
    val minutes = (total % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    val seconds = total % SECONDS_PER_MINUTE
    return if (hours > 0) {
        String.format(java.util.Locale.ROOT, "%d:%02d:%02d", hours, minutes, seconds)
    } else {
        String.format(java.util.Locale.ROOT, "%d:%02d", minutes, seconds)
    }
}

private const val MILLIS_PER_SECOND = 1000L
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3600L
