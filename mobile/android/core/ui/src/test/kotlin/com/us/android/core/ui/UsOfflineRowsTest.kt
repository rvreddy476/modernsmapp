package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A video's offline rows on the More sheet (2026-10-02).
 *
 * founder, 2026-10-02: "Keep a copy is not direct download. It should be
 * like to see offline in the app only." What this protects: the rows a
 * viewer gets for each state of a copy (Save offline, Cancel offline save,
 * Remove offline copy, and never two of them); "Offline", the way to the
 * list, on every video whose host keeps copies; the SAME rows on a reel and
 * a long video, for the owner and for everyone else; the rows sitting in
 * the sheet's alphabetical order; no row that says "download"; and a host
 * that keeps no copies (a feed card, or a video host that passed nothing)
 * showing exactly the list it showed before.
 */
class UsOfflineRowsTest {

    private fun state(
        offline: UsOfflineMoreState?,
        own: Boolean = false,
        longVideo: Boolean = false,
        video: Boolean = true,
    ) = UsPostMoreState(
        postId = "p1",
        username = "raghu",
        isOwnPost = own,
        isBookmarked = false,
        followRow = UsPostMoreFollowRow.FOLLOW,
        link = postShareLink("p1"),
        reel = if (video) {
            UsReelMoreState(description = "a caption", fullMode = false, qualities = reelQualityOptions(listOf(720)))
        } else {
            null
        },
        longVideo = if (longVideo) UsLongVideoMoreState(channelName = "Raghu Builds") else null,
        offline = offline,
    )

    private fun UsPostMoreState.labels(): List<String> = rows().map { labelOf(it) }

    private val offlineRowSet = setOf(
        UsPostMoreRow.SAVE_OFFLINE,
        UsPostMoreRow.CANCEL_OFFLINE,
        UsPostMoreRow.REMOVE_OFFLINE,
        UsPostMoreRow.OFFLINE_PAGE,
    )

    private fun UsPostMoreState.offlineOnly(): List<UsPostMoreRow> = rows().filter { it in offlineRowSet }

    @Test
    fun `a video that may be saved offers save offline and the way to the list`() {
        val rows = state(UsOfflineMoreState(UsOfflineAction.SAVE)).offlineOnly()

        assertThat(rows).containsExactly(UsPostMoreRow.OFFLINE_PAGE, UsPostMoreRow.SAVE_OFFLINE)
    }

    @Test
    fun `a video the creator has not allowed offers the list and no save`() {
        val rows = state(UsOfflineMoreState(UsOfflineAction.NONE)).offlineOnly()

        assertThat(rows).containsExactly(UsPostMoreRow.OFFLINE_PAGE)
    }

    @Test
    fun `a save in flight offers cancel in save's place`() {
        val rows = state(UsOfflineMoreState(UsOfflineAction.CANCEL, progress = 0.4f)).offlineOnly()

        assertThat(rows).containsExactly(UsPostMoreRow.CANCEL_OFFLINE, UsPostMoreRow.OFFLINE_PAGE)
    }

    @Test
    fun `a stored copy offers remove in save's place`() {
        val rows = state(UsOfflineMoreState(UsOfflineAction.REMOVE)).offlineOnly()

        assertThat(rows).containsExactly(UsPostMoreRow.OFFLINE_PAGE, UsPostMoreRow.REMOVE_OFFLINE)
    }

    @Test
    fun `the owner gets the same offline rows as everyone else`() {
        for (action in UsOfflineAction.entries) {
            val offline = UsOfflineMoreState(action)

            assertThat(state(offline, own = true).offlineOnly()).isEqualTo(state(offline, own = false).offlineOnly())
        }
    }

    @Test
    fun `a reel and a long video offer the same offline rows`() {
        for (action in UsOfflineAction.entries) {
            val offline = UsOfflineMoreState(action)

            assertThat(state(offline, longVideo = true).rows()).isEqualTo(state(offline, longVideo = false).rows())
        }
    }

    @Test
    fun `the offline rows sit in the sheet's alphabetical order`() {
        val labels = state(UsOfflineMoreState(UsOfflineAction.SAVE)).labels()

        assertThat(labels).containsExactly(
            "Block channel",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Offline",
            "Quality",
            "Report",
            "Save offline",
            "Share",
        ).inOrder()
        for (action in UsOfflineAction.entries) {
            assertThat(state(UsOfflineMoreState(action)).labels().map { it.lowercase() }).isInStrictOrder()
        }
    }

    @Test
    fun `no offline row ever says download`() {
        for (row in offlineRowSet) {
            assertThat(row.label.lowercase()).doesNotContain("download")
            assertThat(row.label.lowercase()).doesNotContain("file")
        }
        assertThat(offlineRowSet.map { it.label })
            .containsExactly("Save offline", "Cancel offline save", "Remove offline copy", "Offline")
    }

    @Test
    fun `a host that keeps no offline copies shows the list it showed before`() {
        assertThat(state(offline = null).offlineOnly()).isEmpty()
        assertThat(state(offline = null).labels()).containsExactly(
            "Block channel",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
            "Share",
        ).inOrder()
    }

    @Test
    fun `a feed post never shows an offline row, whatever it is handed`() {
        val post = state(UsOfflineMoreState(UsOfflineAction.SAVE), video = false)

        assertThat(post.surface).isEqualTo(UsPostMoreSurface.POST)
        assertThat(post.offlineOnly()).isEmpty()
    }

    @Test
    fun `the cancel row says how far the save is, or what it is held for`() {
        assertThat(offlineProgressText(UsOfflineMoreState(UsOfflineAction.CANCEL, progress = 0.42f))).isEqualTo("42%")
        assertThat(offlineProgressText(UsOfflineMoreState(UsOfflineAction.CANCEL, progress = null)))
            .isEqualTo("Starting")
        assertThat(
            offlineProgressText(
                UsOfflineMoreState(UsOfflineAction.CANCEL, progress = 0.1f, waiting = "Waiting for Wi-Fi"),
            ),
        ).isEqualTo("Waiting for Wi-Fi")
        assertThat(offlineProgressText(UsOfflineMoreState(UsOfflineAction.CANCEL, progress = 7f))).isEqualTo("100%")
    }
}
