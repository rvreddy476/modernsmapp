package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/** Chapters (2026-10-24): the web's chapters.ts rules, one by one. */
class VideoChaptersTest {

    private val chapters = listOf(
        VideoChapter(0, "Intro"),
        VideoChapter(65_000, "Setup"),
        VideoChapter(600_000, "Build"),
    )

    @Test
    fun `normalising sorts, drops negatives and repeats, and names a blank title by its clock`() {
        val raw = listOf(
            VideoChapter(65_000, "  Setup  "),
            VideoChapter(-1, "never"),
            VideoChapter(0, ""),
            VideoChapter(65_000, "second at the same time"),
            VideoChapter(3_723_000, "Late"),
        )
        assertThat(normalizeChapters(raw)).containsExactly(
            VideoChapter(0, "0:00"),
            VideoChapter(65_000, "Setup"),
            VideoChapter(3_723_000, "Late"),
        ).inOrder()
        assertThat(normalizeChapters(emptyList())).isEmpty()
    }

    @Test
    fun `the chapter playing is the last one started`() {
        assertThat(chapterIndexAt(chapters, 0)).isEqualTo(0)
        assertThat(chapterIndexAt(chapters, 64_999)).isEqualTo(0)
        assertThat(chapterIndexAt(chapters, 65_000)).isEqualTo(1)
        assertThat(chapterIndexAt(chapters, 599_999)).isEqualTo(1)
        assertThat(chapterIndexAt(chapters, 9_999_999)).isEqualTo(2)
        assertThat(chapterAt(chapters, 70_000)?.title).isEqualTo("Setup")
        assertThat(chapterIndexAt(emptyList(), 5)).isEqualTo(-1)
        val late = listOf(VideoChapter(10_000, "Later"))
        assertThat(chapterIndexAt(late, 9_999)).isEqualTo(-1)
        assertThat(chapterAt(late, 9_999)).isNull()
    }

    @Test
    fun `ticks skip the start and anything at or past the end, and need a length`() {
        assertThat(chapterTicks(chapters, 1_200_000)).containsExactly(65_000f / 1_200_000, 0.5f).inOrder()
        assertThat(chapterTicks(chapters, 600_000)).containsExactly(65_000f / 600_000)
        assertThat(chapterTicks(chapters, 0)).isEmpty()
        assertThat(chapterTicks(chapters, -1)).isEmpty()
    }

    @Test
    fun `the clock is m ss under an hour and h mm ss from it`() {
        assertThat(formatChapterClock(0)).isEqualTo("0:00")
        assertThat(formatChapterClock(5_999)).isEqualTo("0:05")
        assertThat(formatChapterClock(65_000)).isEqualTo("1:05")
        assertThat(formatChapterClock(3_599_000)).isEqualTo("59:59")
        assertThat(formatChapterClock(3_600_000)).isEqualTo("1:00:00")
        assertThat(formatChapterClock(3_723_000)).isEqualTo("1:02:03")
        assertThat(formatChapterClock(-5)).isEqualTo("0:00")
    }
}
