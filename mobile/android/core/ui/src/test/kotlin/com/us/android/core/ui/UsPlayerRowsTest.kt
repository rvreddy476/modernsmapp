package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The player's rows of a video's More sheet (TikTok parity T-32,
 * 2026-10-15): Playback speed, Captions and — on a reel — Auto scroll.
 *
 * What is pinned: the rows are in the ONE alphabetical list like every other
 * row (founder, 2026-09-30); Captions is withheld when the video has no
 * caption tracks; Auto scroll is a reel's alone; a host that offers no speeds
 * gets the list it had; and the values the rows show at their right.
 */
class UsPlayerRowsTest {

    private fun base(own: Boolean = false) = UsPostMoreState(
        postId = "p1",
        username = "call_userb",
        isOwnPost = own,
        isBookmarked = false,
        followRow = UsPostMoreFollowRow.FOLLOW,
        link = postShareLink("p1"),
    )

    private val english = UsCaptionOption(language = "en", label = "English")
    private val hindi = UsCaptionOption(language = "hi", label = "Hindi")

    /** What Reels hands the sheet: the speeds, the reel's tracks, and the Auto scroll switch. */
    private fun reelPlayer(
        captions: List<UsCaptionOption> = listOf(english),
        autoScroll: Boolean = false,
        speed: Float = SPEED_NORMAL,
        captionLanguage: String? = null,
        canUseSound: Boolean = false,
    ) = UsReelMoreState(
        description = "sunday at the lake",
        fullMode = false,
        qualities = reelQualityOptions(listOf(360, 720)),
        canUseSound = canUseSound,
        speeds = PLAYBACK_SPEEDS,
        speed = speed,
        captions = captions,
        captionLanguage = captionLanguage,
        autoScroll = autoScroll,
    )

    private fun UsPostMoreState.labels(): List<String> = rows().map { labelOf(it) }

    @Test
    fun `a reel with tracks reads the whole list, the player's rows in their alphabetical places`() {
        val labels = base().copy(reel = reelPlayer(canUseSound = true)).labels()

        assertThat(labels).containsExactly(
            "Auto scroll",
            "Block channel",
            "Captions",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Playback speed",
            "Quality",
            "Report",
            "Share",
            "Use this sound",
        ).inOrder()
    }

    @Test
    fun `the player's rows keep the list alphabetical in every state`() {
        val states = buildList {
            for (own in listOf(false, true)) {
                for (captions in listOf(emptyList(), listOf(english), listOf(english, hindi))) {
                    for (autoScroll in listOf(true, false)) {
                        add(base(own).copy(reel = reelPlayer(captions = captions, autoScroll = autoScroll)))
                    }
                }
                add(base(own).copy(reel = reelPlayer(), longVideo = UsLongVideoMoreState(channelName = "Clee Builds")))
            }
        }
        states.forEach { state ->
            val labels = state.labels()
            assertThat(labels).containsNoDuplicates()
            labels.zipWithNext().forEach { (before, after) ->
                assertThat(before.lowercase()).isLessThan(after.lowercase())
            }
        }
    }

    @Test
    fun `captions is withheld when the video has no caption tracks`() {
        val rows = base().copy(reel = reelPlayer(captions = emptyList())).rows()

        assertThat(rows).doesNotContain(UsPostMoreRow.CAPTIONS)
        assertThat(rows).contains(UsPostMoreRow.PLAYBACK_SPEED)
        assertThat(rows).contains(UsPostMoreRow.AUTO_SCROLL)
    }

    @Test
    fun `captions is offered as soon as there is one track, on the owner's video too`() {
        assertThat(base().copy(reel = reelPlayer(captions = listOf(english))).rows()).contains(UsPostMoreRow.CAPTIONS)
        assertThat(base(own = true).copy(reel = reelPlayer()).rows())
            .containsAtLeast(UsPostMoreRow.CAPTIONS, UsPostMoreRow.PLAYBACK_SPEED, UsPostMoreRow.AUTO_SCROLL)
    }

    /** A long video has no next reel: speed and captions like a reel, no Auto scroll. */
    @Test
    fun `a long video offers speed and captions but never auto scroll`() {
        val longVideo = base().copy(
            reel = reelPlayer().copy(autoScroll = null),
            longVideo = UsLongVideoMoreState(channelName = "Clee Builds"),
        )

        assertThat(longVideo.rows()).containsAtLeast(UsPostMoreRow.PLAYBACK_SPEED, UsPostMoreRow.CAPTIONS)
        assertThat(longVideo.rows()).doesNotContain(UsPostMoreRow.AUTO_SCROLL)
    }

    /** The hosts that set nothing new (and every test written before T-32) keep the list they had. */
    @Test
    fun `a video whose host offers no speeds has none of the player's rows`() {
        val rows = base().copy(reel = UsReelMoreState(description = "", fullMode = false, qualities = emptyList())).rows()

        assertThat(rows).containsNoneOf(UsPostMoreRow.PLAYBACK_SPEED, UsPostMoreRow.CAPTIONS, UsPostMoreRow.AUTO_SCROLL)
    }

    @Test
    fun `a feed post never has the player's rows`() {
        assertThat(base().rows())
            .containsNoneOf(UsPostMoreRow.PLAYBACK_SPEED, UsPostMoreRow.CAPTIONS, UsPostMoreRow.AUTO_SCROLL)
    }

    @Test
    fun `the speeds are the long video's six, slowest first, printed without trailing zeros`() {
        assertThat(PLAYBACK_SPEEDS).containsExactly(0.5f, 0.75f, 1f, 1.25f, 1.5f, 2f).inOrder()
        assertThat(PLAYBACK_SPEEDS.map(::speedLabel))
            .containsExactly("0.5x", "0.75x", "1x", "1.25x", "1.5x", "2x").inOrder()
    }

    @Test
    fun `the captions row reads the track that is on, else off`() {
        val tracks = listOf(english, hindi)

        assertThat(captionValueLabel(tracks, null)).isEqualTo("Off")
        assertThat(captionValueLabel(tracks, "hi")).isEqualTo("Hindi")
        // A language the video does not have is off, not a blank.
        assertThat(captionValueLabel(tracks, "fr")).isEqualTo("Off")
    }
}
