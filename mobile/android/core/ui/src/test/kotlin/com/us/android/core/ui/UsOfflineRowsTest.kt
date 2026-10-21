package com.us.android.core.ui

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * A video's offline row on the More sheet (2026-10-02).
 *
 * founder, 2026-10-02: "Keep a copy is not direct download. It should be
 * like to see offline in the app only." What this protects: the ONE row a
 * viewer gets for each state of a copy (Save offline, Cancel offline save,
 * Remove offline copy, never two of them, and since 2026-10-17 never an
 * "Offline" list row beside it); the SAME rows on a reel and a long video,
 * for the owner and for everyone else; the rows sitting in the sheet's
 * alphabetical order; no row that says "download"; and a host that keeps no
 * copies (a feed card, or a video host that passed nothing) showing exactly
 * the list it showed before.
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
    )

    private fun UsPostMoreState.offlineOnly(): List<UsPostMoreRow> = rows().filter { it in offlineRowSet }

    @Test
    fun `a video that may be saved offers no save offline row`() {
        // Founder, 2026-10-19: "Save offline" is gone from the sheet.
        val rows = state(UsOfflineMoreState(UsOfflineAction.SAVE)).offlineOnly()

        assertThat(rows).isEmpty()
    }

    @Test
    fun `a video the creator has not allowed offers no offline row`() {
        val rows = state(UsOfflineMoreState(UsOfflineAction.NONE)).offlineOnly()

        assertThat(rows).isEmpty()
    }

    /** Founder, 2026-10-21: no offline row on a video's More at all, as on the web; copies are managed on the Offline page. */
    @Test
    fun `a save in flight or a stored copy offers no offline row either`() {
        val inFlight = state(UsOfflineMoreState(UsOfflineAction.CANCEL, progress = 0.4f)).offlineOnly()
        val stored = state(UsOfflineMoreState(UsOfflineAction.REMOVE)).offlineOnly()

        assertThat(inFlight).isEmpty()
        assertThat(stored).isEmpty()
    }

    /**
     * The duplicate the 2026-10-17 walk found: "Offline" and "Save offline"
     * side by side with the same download glyph. The list of copies is not
     * on a video's sheet at all now.
     */
    @Test
    fun `no state ever shows two offline rows or a row named Offline`() {
        for (action in UsOfflineAction.entries) {
            val sheet = state(UsOfflineMoreState(action))

            assertThat(sheet.offlineOnly().size).isAtMost(1)
            assertThat(sheet.labels()).doesNotContain("Offline")
        }
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
    fun `the sheet stays alphabetical and never carries an offline row`() {
        val labels = state(UsOfflineMoreState(UsOfflineAction.REMOVE)).labels()

        assertThat(labels).containsExactly(
            "Block @raghu",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
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
            .containsExactly("Save offline", "Cancel offline save", "Remove offline copy")
    }

    @Test
    fun `a host that keeps no offline copies shows the list it showed before`() {
        assertThat(state(offline = null).offlineOnly()).isEmpty()
        assertThat(state(offline = null).labels()).containsExactly(
            "Block @raghu",
            "Copy link",
            "Description",
            "Don't recommend this channel",
            "Not interested",
            "Quality",
            "Report",
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
