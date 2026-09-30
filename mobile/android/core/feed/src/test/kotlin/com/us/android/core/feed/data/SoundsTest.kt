package com.us.android.core.feed.data

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.feed.data.dto.FeedCountsDto
import com.us.android.core.feed.data.dto.FeedItemDto
import com.us.android.core.feed.data.dto.FeedMediaDto
import com.us.android.core.feed.data.dto.FeedSoundDto
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.di.NetworkModule
import org.junit.Test

/**
 * The sound rules the screens share: what a feed row's sound fields become,
 * what a page of a sound's reels holds, and what the viewer reads.
 *
 * What these protect: `audio_track_id` alone never starts a second audio
 * stream; a creator's volume of 0 survives the trip from the wire; the origin
 * is never drawn twice on the sound page; and a refusal is worded by its
 * code, in one place.
 */
class SoundsTest {

    private val json = NetworkModule.provideJson()

    private fun flick(
        id: String = "p1",
        durationMs: Long = 20_000L,
        contentType: String = "flick",
        sound: FeedSoundDto? = null,
    ) = FeedItemDto(
        id = id,
        authorId = "a1",
        contentType = contentType,
        media = listOf(FeedMediaDto(mediaId = "m-$id", kind = "video", durationMs = durationMs)),
        sound = sound,
    )

    // ── a feed row ──────────────────────────────────────────────────────

    @Test
    fun `audio_track_id alone is not a sound, the reel plays its own audio`() {
        val row = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p1","author_id":"a1","audio_track_id":"s1","audio_start_ms":1200}""",
        )

        assertThat(row.audioTrackId).isEqualTo("s1")
        assertThat(row.toDomain().sound).isNull()
    }

    @Test
    fun `a sound with an empty id is no sound`() {
        assertThat(flick(sound = FeedSoundDto(id = "", title = "x")).toDomain().sound).isNull()
        assertThat(flick(sound = FeedSoundDto(id = "  ")).toDomain().sound).isNull()
    }

    @Test
    fun `a sound start of 0 falls back to the post's audio_start_ms`() {
        val row = flick(sound = FeedSoundDto(id = "s1", startMs = 0)).copy(audioStartMs = 1_500L)

        assertThat(row.toDomain().sound?.startMs).isEqualTo(1_500L)
        assertThat(row.copy(sound = FeedSoundDto(id = "s1", startMs = 800)).toDomain().sound?.startMs).isEqualTo(800L)
    }

    @Test
    fun `an absent volume is 1 and a present 0 is 0`() {
        val absent = json.decodeFromString(FeedItemDto.serializer(), """{"id":"p1"}""").toDomain()
        val muted = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p1","original_audio_volume":0,"overlay_audio_volume":0}""",
        ).toDomain()
        val loud = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p1","original_audio_volume":1.7,"overlay_audio_volume":-0.2}""",
        ).toDomain()
        val nulls = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p1","original_audio_volume":null,"overlay_audio_volume":null}""",
        ).toDomain()

        assertThat(absent.originalVolume).isEqualTo(1.0)
        assertThat(absent.overlayVolume).isEqualTo(1.0)
        assertThat(muted.originalVolume).isEqualTo(0.0)
        assertThat(muted.overlayVolume).isEqualTo(0.0)
        assertThat(loud.originalVolume).isEqualTo(1.0)
        assertThat(loud.overlayVolume).isEqualTo(0.0)
        assertThat(nulls.originalVolume).isEqualTo(1.0)
        assertThat(nulls.overlayVolume).isEqualTo(1.0)
    }

    @Test
    fun `only disallow turns reuse off`() {
        assertThat(flick().copy(remixSetting = "disallow").toDomain().soundReuseAllowed).isFalse()
        assertThat(flick().copy(remixSetting = "allow").toDomain().soundReuseAllowed).isTrue()
        assertThat(flick().copy(remixSetting = "allow_audio_only").toDomain().soundReuseAllowed).isTrue()
        assertThat(flick().copy(remixSetting = "").toDomain().soundReuseAllowed).isTrue()
    }

    @Test
    fun `share and save counts are read when the row carries them and absent when it does not`() {
        val none = flick().toDomain().counts
        val both = flick().copy(counts = FeedCountsDto(likes = 1, comments = 2, shares = 3, saves = 4))
            .toDomain().counts
        val older = flick().copy(counts = FeedCountsDto(bookmarks = 9)).toDomain().counts
        val wire = json.decodeFromString(
            FeedItemDto.serializer(),
            """{"id":"p1","counts":{"likes":12,"comments":3,"shares":0}}""",
        ).toDomain().counts

        assertThat(none.shares).isNull()
        assertThat(none.saves).isNull()
        assertThat(both.shares).isEqualTo(3)
        assertThat(both.saves).isEqualTo(4)
        assertThat(older.saves).isEqualTo(9)
        assertThat(wire.shares).isEqualTo(0)
        assertThat(wire.saves).isNull()
    }

    // ── a page of a sound's reels ───────────────────────────────────────

    private fun page(origin: FeedItemDto?, items: List<FeedItemDto>, cursor: String? = null) =
        SoundReelsDto(sound = FeedSoundDto(id = "s1", useCount = 3), origin = origin, items = items)
            .toPage(ApiMeta(nextCursor = cursor))

    @Test
    fun `the origin is never repeated among the items`() {
        val result = page(origin = flick("o"), items = listOf(flick("a"), flick("o"), flick("b")))

        assertThat(result.origin?.id).isEqualTo("o")
        assertThat(result.items.map { it.id }).containsExactly("a", "b").inOrder()
    }

    @Test
    fun `a row that is not a reel is dropped, and so is a deleted one and one without an id`() {
        val result = page(
            origin = null,
            items = listOf(
                flick("a"),
                flick("long", contentType = "long_video"),
                flick("post", contentType = "post"),
                flick("over", durationMs = 5L * 60L * 1_000L + 1L),
                flick("gone").copy(deletedAt = "2026-09-29T10:00:00Z"),
                flick(""),
                flick("image").copy(media = listOf(FeedMediaDto(mediaId = "i", kind = "image"))),
                flick("reel", contentType = "REEL"),
            ),
        )

        assertThat(result.items.map { it.id }).containsExactly("a", "reel").inOrder()
    }

    @Test
    fun `an origin that is not a reel is no origin`() {
        assertThat(page(origin = flick("o", contentType = "long_video"), items = emptyList()).origin).isNull()
        assertThat(page(origin = flick(""), items = emptyList()).origin).isNull()
    }

    @Test
    fun `an empty cursor is the end, like an absent one`() {
        assertThat(page(origin = null, items = listOf(flick("a")), cursor = "").nextCursor).isNull()
        assertThat(page(origin = null, items = listOf(flick("a")), cursor = null).nextCursor).isNull()
        assertThat(page(origin = null, items = listOf(flick("a")), cursor = "abc").nextCursor).isEqualTo("abc")
        assertThat(SoundReelsDto().toPage(meta = null).nextCursor).isNull()
    }

    @Test
    fun `tiles are every page in order, the origin first and marked, nothing twice`() {
        val first = page(origin = flick("o"), items = listOf(flick("a"), flick("b")), cursor = "c1")
        val second = page(origin = null, items = listOf(flick("b"), flick("o"), flick("c")))

        val tiles = soundReelTiles(listOf(first, second))

        assertThat(tiles.map { it.reel.id }).containsExactly("o", "a", "b", "c").inOrder()
        assertThat(tiles.map { it.isOrigin }).containsExactly(true, false, false, false).inOrder()
        assertThat(soundReelTiles(emptyList())).isEmpty()
    }

    /** The origin is the FIRST page's: a later page that names one is not given the first tile. */
    @Test
    fun `only the first page's origin leads the tiles`() {
        val first = page(origin = null, items = listOf(flick("a")))
        val second = page(origin = flick("o"), items = listOf(flick("b")))

        assertThat(soundReelTiles(listOf(first, second)).map { it.reel.id to it.isOrigin })
            .containsExactly("a" to false, "b" to false).inOrder()
    }

    // ── the sound's own row ─────────────────────────────────────────────

    @Test
    fun `a row is usable when ready, active or without a status, and not otherwise`() {
        fun row(status: String) = SoundRowDto(id = "s1", title = "x", status = status).toReelSound()

        assertThat(row("ready")).isNotNull()
        assertThat(row("active")).isNotNull()
        assertThat(row("")).isNotNull()
        assertThat(row(" READY ")).isNotNull()
        assertThat(row("processing")).isNull()
        assertThat(row("failed")).isNull()
        assertThat(SoundRowDto(id = "", status = "ready").toReelSound()).isNull()
    }

    @Test
    fun `a row is read in media-service's spelling and starts at 0`() {
        val sound = SoundRowDto(id = "s1", usageCount = 4, sourceReelId = "p9", durationMs = 9_000L).toReelSound()!!

        assertThat(sound.useCount).isEqualTo(4)
        assertThat(sound.sourcePostId).isEqualTo("p9")
        assertThat(sound.startMs).isEqualTo(0L)
        assertThat(sound.title).isEqualTo("Original sound")
    }

    // ── words ───────────────────────────────────────────────────────────

    @Test
    fun `Original sound - author, with a plain hyphen`() {
        assertThat(originalSoundLabel("Asha")).isEqualTo("Original sound - Asha")
        assertThat(originalSoundLabel("  Asha  ")).isEqualTo("Original sound - Asha")
        assertThat(originalSoundLabel("  ")).isEqualTo("Original sound")
        assertThat(originalSoundLabel("")).isEqualTo("Original sound")
    }

    @Test
    fun `reel counts`() {
        assertThat(soundReelCount(0)).isEqualTo("No reels yet")
        assertThat(soundReelCount(-4)).isEqualTo("No reels yet")
        assertThat(soundReelCount(1)).isEqualTo("1 reel")
        assertThat(soundReelCount(12)).isEqualTo("12 reels")
        assertThat(soundReelCount(1_200)).isEqualTo("1,200 reels")
    }

    @Test
    fun `a refusal is explained by its code, in the web's words`() {
        assertThat(soundRefusalMessage("SOUND_REUSE_NOT_ALLOWED"))
            .isEqualTo("The creator has turned off reuse for this reel.")
        assertThat(soundRefusalMessage("NOT_READY")).isEqualTo("This reel is still processing. Try again in a moment.")
        for (code in listOf("TOO_LONG", "NOT_A_REEL", "NOT_A_VIDEO")) {
            assertThat(soundRefusalMessage(code)).isEqualTo("Only reels up to 5 minutes can be used as a sound.")
        }
        assertThat(soundRefusalMessage("NO_AUDIO")).isEqualTo("This reel has no sound to use.")
        assertThat(soundRefusalMessage("NOT_FOUND")).isEqualTo("This reel is no longer available.")
        assertThat(soundRefusalMessage("404")).isEqualTo("This reel is no longer available.")
        assertThat(soundRefusalMessage("RATE_LIMITED"))
            .isEqualTo("That is a lot of sounds in one hour. Try again later.")
        assertThat(soundRefusalMessage("SOUND_UNAVAILABLE")).isEqualTo("Please try again.")
        assertThat(soundRefusalMessage(null)).isEqualTo("Please try again.")
        assertThat(SOUND_GONE_MESSAGE).isEqualTo("This sound is no longer available.")
    }

    @Test
    fun `the code is read from whichever case the mapper chose`() {
        assertThat(AppError.Forbidden(code = "SOUND_REUSE_NOT_ALLOWED").soundRefusalCode())
            .isEqualTo("SOUND_REUSE_NOT_ALLOWED")
        assertThat(AppError.NotFound().soundRefusalCode()).isEqualTo("NOT_FOUND")
        assertThat(AppError.RateLimited(retryAfterSeconds = null).soundRefusalCode()).isEqualTo("RATE_LIMITED")
        assertThat(AppError.Server(statusCode = 503, code = "SOUND_UNAVAILABLE").soundRefusalCode())
            .isEqualTo("SOUND_UNAVAILABLE")
        assertThat(AppError.Unknown(code = "NOT_READY", statusCode = 422).soundRefusalCode()).isEqualTo("NOT_READY")
        assertThat(AppError.NoNetwork().soundRefusalCode()).isNull()
        assertThat(AppError.NoNetwork().soundRefusalMessage()).isEqualTo("Please try again.")
    }
}
