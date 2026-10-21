package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A video's dub and owner rows (2026-10-21, the web's `moreRows.ts`):
 * "Audio track" (shared — the owner's too — only with more than one track,
 * the original counting as one), and the owner's "Audio tracks" and "Edit"
 * (context rows, only where the host has those sheets). Everything stays in
 * the ONE alphabetical list.
 */
class UsVideoDubAndEditRowsTest {

    private fun base(own: Boolean = false) = UsPostMoreState(
        postId = "p1",
        username = "call_userb",
        channelName = "Clee Builds",
        isOwnPost = own,
        isBookmarked = false,
        followRow = UsPostMoreFollowRow.FOLLOW,
        link = postShareLink("p1"),
    )

    private val original = UsAudioTrackOption("original", "Original")
    private val hindi = UsAudioTrackOption("t-hi", "Hindi")

    private fun video(
        tracks: List<UsAudioTrackOption> = emptyList(),
        canEdit: Boolean = false,
        canManageAudio: Boolean = false,
        autoScroll: Boolean? = null,
    ) = UsReelMoreState(
        description = "sunday at the lake",
        fullMode = false,
        qualities = reelQualityOptions(listOf(360, 720)),
        speeds = PLAYBACK_SPEEDS,
        autoScroll = autoScroll,
        audioTracks = tracks,
        audioTrackId = tracks.firstOrNull()?.id,
        canEdit = canEdit,
        canManageAudio = canManageAudio,
    )

    private fun UsPostMoreState.labels(): List<String> = rows().map { labelOf(it) }

    @Test
    fun `audio track needs the original and at least one dub`() {
        assertThat(base().copy(reel = video()).rows()).doesNotContain(UsPostMoreRow.AUDIO_TRACK)
        assertThat(base().copy(reel = video(listOf(original))).rows()).doesNotContain(UsPostMoreRow.AUDIO_TRACK)
        assertThat(base().copy(reel = video(listOf(original, hindi))).rows()).contains(UsPostMoreRow.AUDIO_TRACK)
    }

    @Test
    fun `audio track is shared, so the owner gets it too`() {
        assertThat(base(own = true).copy(reel = video(listOf(original, hindi))).rows())
            .contains(UsPostMoreRow.AUDIO_TRACK)
    }

    @Test
    fun `a viewer never gets the owner's rows, whatever the host offers`() {
        val rows = base().copy(reel = video(canEdit = true, canManageAudio = true)).rows()

        assertThat(rows).containsNoneOf(UsPostMoreRow.EDIT, UsPostMoreRow.MANAGE_AUDIO, UsPostMoreRow.DELETE)
    }

    @Test
    fun `the owner's rows appear only where the host has their sheets`() {
        assertThat(base(own = true).copy(reel = video()).rows())
            .containsNoneOf(UsPostMoreRow.EDIT, UsPostMoreRow.MANAGE_AUDIO)
        assertThat(base(own = true).copy(reel = video(canEdit = true)).rows()).contains(UsPostMoreRow.EDIT)
        assertThat(base(own = true).copy(reel = video(canManageAudio = true)).rows())
            .contains(UsPostMoreRow.MANAGE_AUDIO)
    }

    @Test
    fun `the owner's reel reads the web's list, alphabetical`() {
        val labels = base(own = true)
            .copy(reel = video(listOf(original, hindi), canEdit = true, canManageAudio = true, autoScroll = false))
            .labels()

        assertThat(labels).containsExactly(
            "Audio track",
            "Audio tracks",
            "Auto scroll",
            "Copy link",
            "Delete",
            "Description",
            "Edit",
            "Playback speed",
            "Quality",
        ).inOrder()
    }

    @Test
    fun `a viewer's long video with a dub reads the web's list, alphabetical`() {
        val labels = base()
            .copy(
                reel = video(listOf(original, hindi), canEdit = true, canManageAudio = true),
                longVideo = UsLongVideoMoreState(channelName = "Clee Builds"),
            )
            .labels()

        assertThat(labels).containsExactly(
            "Audio track",
            "Block Clee Builds",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Playback speed",
            "Quality",
            "Report",
        ).inOrder()
    }

    @Test
    fun `every combination keeps one alphabetical list with no repeats`() {
        val states = buildList {
            for (own in listOf(false, true)) {
                for (tracks in listOf(emptyList(), listOf(original), listOf(original, hindi))) {
                    for (edit in listOf(false, true)) {
                        for (manage in listOf(false, true)) {
                            add(base(own).copy(reel = video(tracks, edit, manage)))
                            add(
                                base(own).copy(
                                    reel = video(tracks, edit, manage),
                                    longVideo = UsLongVideoMoreState(channelName = "Clee Builds"),
                                ),
                            )
                        }
                    }
                }
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
    fun `a feed post never has the dub or edit rows`() {
        assertThat(base(own = true).rows())
            .containsNoneOf(UsPostMoreRow.AUDIO_TRACK, UsPostMoreRow.MANAGE_AUDIO, UsPostMoreRow.EDIT)
    }

    @Test
    fun `the audio track row reads the track playing, else the original`() {
        val options = listOf(original, hindi)

        assertThat(audioTrackValueLabel(options, "t-hi")).isEqualTo("Hindi")
        assertThat(audioTrackValueLabel(options, null)).isEqualTo("Original")
        assertThat(audioTrackValueLabel(options, "gone")).isEqualTo("Original")
        assertThat(audioTrackValueLabel(emptyList(), null)).isEmpty()
    }
}
