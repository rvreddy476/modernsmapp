package com.us.android.feature.feed.ui.reels

import com.google.common.truth.Truth.assertThat
import com.us.android.core.media.ChosenSound
import com.us.android.core.media.sound.SoundMix
import com.us.android.core.media.sound.SoundTrack
import com.us.android.core.model.FeedAuthor
import com.us.android.core.model.FeedChannel
import com.us.android.core.model.FeedCounts
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedViewerState
import com.us.android.core.model.ReelSound
import org.junit.Test

/**
 * What the Reels screen reads from a reel's sound: the line under the
 * hashtags, the next step of "use this sound", and what the sound player is
 * handed.
 *
 * What these protect: a sound line is drawn only when it can be followed —
 * never on a reel whose creator turned reuse off, never while it is still
 * processing; a reel that already plays a sound never costs a request to use
 * it; and the player is handed the reel's OWN start offset and levels, so a
 * creator who muted the original is obeyed.
 */
class ReelSoundsTest {

    private val added = ReelSound(
        id = "s1",
        title = "Original sound - Asha",
        artist = "Asha",
        startMs = 1_500L,
        durationMs = 28_400L,
        useCount = 3,
        sourcePostId = "p0",
        creatorUserId = "u0",
    )

    private fun reel(
        sound: ReelSound? = null,
        reuse: Boolean = true,
        processing: Boolean = false,
        title: String = "",
        text: String = "",
        hashtags: List<String> = emptyList(),
        channel: FeedChannel? = null,
    ) = FeedItem(
        id = "p1",
        authorId = "a1",
        author = FeedAuthor(id = "a1", displayName = "Ravi", username = "ravi"),
        text = text,
        title = title,
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
        channel = channel,
        hashtags = hashtags,
        sound = sound,
        originalVolume = 0.2,
        overlayVolume = 0.8,
        soundReuseAllowed = reuse,
    )

    // ── The sound line ──────────────────────────────────────────────────

    @Test
    fun `a reel that plays an added sound names it, and the line opens its page`() {
        assertThat(soundLine(reel(sound = added), isOwn = false))
            .isEqualTo(SoundLine(label = "Original sound - Asha", added = true))
        // Whatever the reel's own setting: the sound it plays is someone else's.
        assertThat(soundLine(reel(sound = added, reuse = false, processing = true), isOwn = false)?.added).isTrue()
    }

    @Test
    fun `a reel that plays its own audio reads Original sound - creator when it may be reused`() {
        assertThat(soundLine(reel(), isOwn = false))
            .isEqualTo(SoundLine(label = "Original sound - Ravi", added = false))
    }

    /** The server names the sound after the channel when the reel carries one; the line says the same. */
    @Test
    fun `the creator is the channel when the reel carries one`() {
        val channel = FeedChannel(userId = "a1", name = "Ravi Cooks", handle = "ravicooks")

        assertThat(soundLine(reel(channel = channel), isOwn = false)?.label).isEqualTo("Original sound - Ravi Cooks")
    }

    @Test
    fun `there is no line when the creator turned reuse off, unless the reel is the viewer's own`() {
        assertThat(soundLine(reel(reuse = false), isOwn = false)).isNull()
        assertThat(soundLine(reel(reuse = false), isOwn = true))
            .isEqualTo(SoundLine(label = "Original sound - Ravi", added = false))
    }

    @Test
    fun `there is no line while the reel is still processing`() {
        assertThat(soundLine(reel(processing = true), isOwn = false)).isNull()
        assertThat(soundLine(reel(processing = true), isOwn = true)).isNull()
    }

    @Test
    fun `a reel is the viewer's own only for a known viewer`() {
        assertThat(reel().isOwnedBy("a1")).isTrue()
        assertThat(reel().isOwnedBy("someone-else")).isFalse()
        assertThat(reel().isOwnedBy("")).isFalse()
    }

    // ── Use this sound ──────────────────────────────────────────────────

    @Test
    fun `a reel that plays a sound offers that sound, and one that plays its own audio is resolved first`() {
        assertThat(nextSoundStep(reel(sound = added))).isEqualTo(SoundStep.Open(added))
        assertThat(nextSoundStep(reel())).isEqualTo(SoundStep.Resolve("p1"))
    }

    @Test
    fun `the destination is the create flow or the sound's page`() {
        assertThat(soundDestination(SoundIntent.CREATE, added)).isEqualTo(SoundDestination.Create)
        assertThat(soundDestination(SoundIntent.PAGE, added)).isEqualTo(SoundDestination.Page("s1"))
    }

    /** There is no offset picker: a sound chosen for a NEW reel starts at 0, wherever it started in this one. */
    @Test
    fun `a chosen sound carries its name and length, and no start`() {
        assertThat(added.toChosenSound()).isEqualTo(
            ChosenSound(id = "s1", title = "Original sound - Asha", artist = "Asha", durationMs = 28_400L),
        )
    }

    // ── What the player is handed ───────────────────────────────────────

    @Test
    fun `the player is handed the sound, where it starts and how long it is`() {
        assertThat(reel(sound = added).soundTrack())
            .isEqualTo(SoundTrack(id = "s1", startMs = 1_500L, durationMs = 28_400L))
        assertThat(reel().soundTrack()).isNull()
    }

    @Test
    fun `the player is handed the creator's two levels`() {
        assertThat(reel(sound = added).soundMix()).isEqualTo(SoundMix(originalVolume = 0.2, overlayVolume = 0.8))
    }

    // ── Title and hashtags ──────────────────────────────────────────────

    @Test
    fun `the title is drawn when there is one that is not the caption again`() {
        assertThat(reelTitle(reel(title = "Monsoon walk", text = "Monsoon walk, my take #reels")))
            .isEqualTo("Monsoon walk")
        assertThat(reelTitle(reel(title = "  Monsoon walk  "))).isEqualTo("Monsoon walk")
        assertThat(reelTitle(reel(title = ""))).isNull()
        assertThat(reelTitle(reel(title = "   "))).isNull()
        assertThat(reelTitle(reel(title = "Monsoon walk", text = " Monsoon walk "))).isNull()
    }

    @Test
    fun `hashtags are chips in the author's order, without blanks, a hash or a repeat`() {
        val tags = reelHashtags(reel(hashtags = listOf("reels", "#monsoon", " ", "Reels", "walk ", "##rain")))

        assertThat(tags).containsExactly("reels", "monsoon", "walk", "rain").inOrder()
        assertThat(reelHashtags(reel())).isEmpty()
    }

    @Test
    fun `a chip prints one hash`() {
        assertThat(hashtagLabel("reels")).isEqualTo("#reels")
        assertThat(hashtagLabel("#reels")).isEqualTo("#reels")
        assertThat(hashtagLabel(" ##reels ")).isEqualTo("#reels")
    }
}
