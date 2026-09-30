package com.us.android.core.model

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A sound off the wire, the creator's two levels, and who is offered "Use
 * this sound" — the web's `toReelSound`, `wireVolume` and `canUseSound`
 * (postbook-ui `features/reels/model.ts`, `sounds.ts`), case for case.
 *
 * What these protect: a Go zero value is "nothing" for an id, a title, a
 * start or a count, so an empty `sound.id` must never become a second audio
 * stream; and a volume of 0 is the ONE zero that is a real value — the
 * creator muted that side — so it must never fall through to full volume.
 */
class ReelSoundTest {

    private fun sound(wire: ReelSoundWire, postStartMs: Long? = null) = wire.toReelSound(postStartMs)

    // ── toReelSound ─────────────────────────────────────────────────────

    @Test
    fun `an empty id is no sound, and so is a blank one, a missing one and no object at all`() {
        assertThat(sound(ReelSoundWire(id = "", title = "x"))).isNull()
        assertThat(sound(ReelSoundWire(id = "   ", title = "x"))).isNull()
        assertThat(sound(ReelSoundWire(title = "x"))).isNull()
        assertThat(sound(ReelSoundWire())).isNull()
        assertThat((null as ReelSoundWire?).toReelSound()).isNull()
        assertThat((null as ReelSoundWire?).toReelSound(postStartMs = 1_500)).isNull()
    }

    @Test
    fun `an empty title reads Original sound, and empty artist and source are empty and null`() {
        assertThat(sound(ReelSoundWire(id = "s1", title = "", artist = "", sourcePostId = ""))).isEqualTo(
            ReelSound(
                id = "s1",
                title = ORIGINAL_SOUND_TITLE,
                artist = "",
                startMs = 0,
                durationMs = 0,
                useCount = 0,
                sourcePostId = null,
                creatorUserId = null,
            ),
        )
        assertThat(ORIGINAL_SOUND_TITLE).isEqualTo("Original sound")
        assertThat(sound(ReelSoundWire(id = "s1", sourcePostId = null))!!.sourcePostId).isNull()
        assertThat(sound(ReelSoundWire(id = "s1", title = "   "))!!.title).isEqualTo("Original sound")
    }

    @Test
    fun `every value is trimmed`() {
        val trimmed = sound(
            ReelSoundWire(
                id = " s1 ",
                title = " Kitchen take ",
                artist = " Asha ",
                sourcePostId = " p1 ",
                creatorUserId = " u1 ",
            ),
        )!!

        assertThat(trimmed.id).isEqualTo("s1")
        assertThat(trimmed.title).isEqualTo("Kitchen take")
        assertThat(trimmed.artist).isEqualTo("Asha")
        assertThat(trimmed.sourcePostId).isEqualTo("p1")
        assertThat(trimmed.creatorUserId).isEqualTo("u1")
    }

    @Test
    fun `a start of 0 falls back to the post's audio_start_ms, and a set start wins`() {
        assertThat(sound(ReelSoundWire(id = "s1", startMs = 0), 1_500)!!.startMs).isEqualTo(1_500)
        assertThat(sound(ReelSoundWire(id = "s1"), 1_500)!!.startMs).isEqualTo(1_500)
        assertThat(sound(ReelSoundWire(id = "s1", startMs = 800), 1_500)!!.startMs).isEqualTo(800)
        assertThat(sound(ReelSoundWire(id = "s1", startMs = 0), 0)!!.startMs).isEqualTo(0)
        assertThat(sound(ReelSoundWire(id = "s1", startMs = -5), null)!!.startMs).isEqualTo(0)
        assertThat(sound(ReelSoundWire(id = "s1", startMs = -5), -9)!!.startMs).isEqualTo(0)
    }

    @Test
    fun `a negative length is no length`() {
        assertThat(sound(ReelSoundWire(id = "s1", durationMs = -1))!!.durationMs).isEqualTo(0)
        assertThat(sound(ReelSoundWire(id = "s1", durationMs = 28_400))!!.durationMs).isEqualTo(28_400)
    }

    @Test
    fun `media-service's spelling of the same row is read, usage_count and source_reel_id`() {
        val row = sound(ReelSoundWire(id = "s1", usageCount = 4, sourceReelId = "p9"))!!
        assertThat(row.useCount).isEqualTo(4)
        assertThat(row.sourcePostId).isEqualTo("p9")

        assertThat(sound(ReelSoundWire(id = "s1", useCount = 2, usageCount = 9))!!.useCount).isEqualTo(2)
        assertThat(sound(ReelSoundWire(id = "s1", useCount = 0, usageCount = 9))!!.useCount).isEqualTo(9)
        assertThat(sound(ReelSoundWire(id = "s1", sourcePostId = "p1", sourceReelId = "p9"))!!.sourcePostId)
            .isEqualTo("p1")
        assertThat(sound(ReelSoundWire(id = "s1", sourcePostId = "", sourceReelId = "p9"))!!.sourcePostId)
            .isEqualTo("p9")
    }

    @Test
    fun `the creator of the source is kept when the server names one`() {
        assertThat(sound(ReelSoundWire(id = "s1", creatorUserId = "u1"))!!.creatorUserId).isEqualTo("u1")
        assertThat(sound(ReelSoundWire(id = "s1", creatorUserId = ""))!!.creatorUserId).isNull()
    }

    // ── wireVolume ──────────────────────────────────────────────────────

    @Test
    fun `an absent volume is 1`() {
        assertThat(wireVolume(null)).isEqualTo(1.0)
    }

    @Test
    fun `a present 0 is 0, the creator muted that side`() {
        assertThat(wireVolume(0.0)).isEqualTo(0.0)
    }

    @Test
    fun `a volume is kept inside 0 to 1, and not a number is 1`() {
        assertThat(wireVolume(0.35)).isEqualTo(0.35)
        assertThat(wireVolume(1.7)).isEqualTo(1.0)
        assertThat(wireVolume(-0.2)).isEqualTo(0.0)
        assertThat(wireVolume(Double.NaN)).isEqualTo(1.0)
        assertThat(wireVolume(Double.POSITIVE_INFINITY)).isEqualTo(1.0)
    }

    // ── reuse ───────────────────────────────────────────────────────────

    @Test
    fun `only disallow turns reuse off, and an empty or unknown value does not`() {
        assertThat(allowsSoundReuse("disallow")).isFalse()
        assertThat(allowsSoundReuse("DISALLOW")).isFalse()
        assertThat(allowsSoundReuse("allow")).isTrue()
        assertThat(allowsSoundReuse("allow_audio_only")).isTrue()
        assertThat(allowsSoundReuse("")).isTrue()
        assertThat(allowsSoundReuse(null)).isTrue()
    }

    private fun reel(
        sound: ReelSound? = null,
        reuse: Boolean = true,
        processing: Boolean = false,
    ) = FeedItem(
        id = "p1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Asha"),
        text = "",
        visibility = "public",
        feedContentType = "flick",
        postType = "video",
        createdAt = "",
        isPinned = false,
        media = emptyList(),
        counts = FeedCounts(0, 0, 0, 0),
        viewer = FeedViewerState(isBookmarked = false, hasReacted = false, hasReposted = false),
        isRepostable = false,
        isProcessing = processing,
        sound = sound,
        soundReuseAllowed = reuse,
    )

    private val added = ReelSoundWire(id = "s1").toReelSound()!!

    @Test
    fun `reuse allowed offers the sound to anyone`() {
        assertThat(reel().canUseSound(isOwn = false)).isTrue()
    }

    @Test
    fun `reuse disallowed offers it to nobody but the author`() {
        assertThat(reel(reuse = false).canUseSound(isOwn = false)).isFalse()
        assertThat(reel(reuse = false).canUseSound(isOwn = true)).isTrue()
    }

    @Test
    fun `a reel that plays an added sound offers that sound, whatever its own setting`() {
        assertThat(reel(sound = added, reuse = false).canUseSound(isOwn = false)).isTrue()
        assertThat(reel(sound = added, reuse = false, processing = true).canUseSound(isOwn = false)).isTrue()
    }

    @Test
    fun `a reel still processing has nothing to take yet, not even for its author`() {
        assertThat(reel(processing = true).canUseSound(isOwn = false)).isFalse()
        assertThat(reel(processing = true).canUseSound(isOwn = true)).isFalse()
    }

    @Test
    fun `a row without the new fields is unchanged, no sound, both levels 1, reuse allowed`() {
        val row = reel()

        assertThat(row.sound).isNull()
        assertThat(row.originalVolume).isEqualTo(1.0)
        assertThat(row.overlayVolume).isEqualTo(1.0)
        assertThat(row.soundReuseAllowed).isTrue()
        assertThat(row.counts.shares).isNull()
        assertThat(row.counts.saves).isNull()
    }
}
