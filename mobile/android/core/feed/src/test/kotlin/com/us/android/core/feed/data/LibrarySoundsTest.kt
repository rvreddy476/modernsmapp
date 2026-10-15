package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

/** The library's pure rules (2026-10-15): genres, the row's words, and a refusal's line. */
class LibrarySoundsTest {

    private fun sound(id: String, genre: String = "") = LibrarySound(
        id = id,
        title = "T",
        artist = "",
        durationMs = 0L,
        genre = genre,
        mood = "",
        usageCount = 0L,
        isTrending = false,
        audioTrackId = null,
        playable = true,
    )

    @Test
    fun `genres seen so far are kept, once each, in order`() {
        val first = libraryGenres(emptyList(), listOf(sound("a", "pop"), sound("b", ""), sound("c", "Ambient")))
        assertThat(first).containsExactly("Ambient", "pop").inOrder()

        // Filtered to one genre, the others stay on offer.
        val later = libraryGenres(first, listOf(sound("d", "POP"), sound("e", "lo-fi")))
        assertThat(later).containsExactly("Ambient", "lo-fi", "pop").inOrder()
    }

    @Test
    fun `a row says what is known of it`() {
        assertThat(genreLabel("lo-fi")).isEqualTo("Lo-fi")
        assertThat(
            librarySoundMeta(sound("a", "pop").copy(artist = "Studio", durationMs = 28_000L, mood = "calm")),
        ).isEqualTo("Studio · 0:28 · Pop · Calm")
        assertThat(librarySoundMeta(sound("a"))).isEmpty()
        assertThat(libraryUsageLabel(0L)).isEmpty()
        assertThat(libraryUsageLabel(1L)).isEqualTo("1 video")
        assertThat(libraryUsageLabel(1_200L)).isEqualTo("1,200 videos")
    }

    @Test
    fun `a refused Use is read by code or status, never by message`() {
        assertThat(AppError.AuthFailed().librarySoundUseFailure()).isEqualTo(LibrarySoundUseFailure.SIGNED_OUT)
        assertThat(AppError.NotFound().librarySoundUseFailure()).isEqualTo(LibrarySoundUseFailure.GONE)
        assertThat(AppError.Unknown(code = "NOT_STORED", statusCode = 422).librarySoundUseFailure())
            .isEqualTo(LibrarySoundUseFailure.NOT_STORED)
        assertThat(AppError.Unknown(code = null, statusCode = 422).librarySoundUseFailure())
            .isEqualTo(LibrarySoundUseFailure.NOT_STORED)
        assertThat(AppError.Server(statusCode = 503, code = null).librarySoundUseFailure())
            .isEqualTo(LibrarySoundUseFailure.UNAVAILABLE)
        assertThat(AppError.Server(statusCode = 500, code = "SOUND_UNAVAILABLE").librarySoundUseFailure())
            .isEqualTo(LibrarySoundUseFailure.UNAVAILABLE)
        assertThat(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR").librarySoundUseFailure())
            .isEqualTo(LibrarySoundUseFailure.OTHER)
        assertThat(AppError.NoNetwork().librarySoundUseFailure()).isEqualTo(LibrarySoundUseFailure.OFFLINE)
        assertThat(AppError.Timeout().librarySoundUseFailure()).isEqualTo(LibrarySoundUseFailure.OFFLINE)
        assertThat(AppError.Malformed("x").librarySoundUseFailure()).isEqualTo(LibrarySoundUseFailure.OTHER)
        assertThat(
            AppError.Unknown(code = "BAD_REQUEST", statusCode = 400, message = "Sound not found").librarySoundUseFailure(),
        ).isEqualTo(LibrarySoundUseFailure.OTHER)
    }

    @Test
    fun `every refusal has its own line`() {
        val lines = LibrarySoundUseFailure.entries.map { it.message }
        assertThat(lines.toSet()).hasSize(lines.size)
        assertThat(lines.none { it.isBlank() }).isTrue()
    }
}
