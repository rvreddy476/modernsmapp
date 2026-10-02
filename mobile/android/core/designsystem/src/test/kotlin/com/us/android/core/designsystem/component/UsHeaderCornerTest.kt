package com.us.android.core.designsystem.component

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.icon.UsIcons
import org.junit.Test
import java.io.File

/**
 * Protects the top-right corner of every section header (founder,
 * 2026-10-02).
 *
 * Two corners, and only two. The STANDARD one, on every header but Home's:
 * Search, Notifications, then More at the corner. HOME'S, which stays
 * exactly as it was: Messages, Notifications, Search, More. The three dots
 * rather than the hamburger, and no create button up there on either.
 *
 * This replaces the single "Search, then More" corner of the same morning:
 * the bell joined every header, and Home became the stated exception.
 */
class UsHeaderCornerTest {

    @Test
    fun `the standard corner is Search, Notifications, then More at the corner`() {
        assertThat(UsHeaderCorner)
            .containsExactly(
                UsHeaderCornerAction.SEARCH,
                UsHeaderCornerAction.NOTIFICATIONS,
                UsHeaderCornerAction.MORE,
            )
            .inOrder()
        assertThat(UsHeaderCorner.last()).isEqualTo(UsHeaderCornerAction.MORE)
    }

    @Test
    fun `Home's corner is Messages, Notifications, Search, then More at the corner`() {
        assertThat(UsHomeHeaderCorner)
            .containsExactly(
                UsHeaderCornerAction.MESSAGES,
                UsHeaderCornerAction.NOTIFICATIONS,
                UsHeaderCornerAction.SEARCH,
                UsHeaderCornerAction.MORE,
            )
            .inOrder()
        assertThat(UsHomeHeaderCorner.last()).isEqualTo(UsHeaderCornerAction.MORE)
    }

    @Test
    fun `Messages is on Home's header and no other`() {
        assertThat(UsHomeHeaderCorner).contains(UsHeaderCornerAction.MESSAGES)
        assertThat(UsHeaderCorner).doesNotContain(UsHeaderCornerAction.MESSAGES)
        // Home is the standard corner plus Messages, and nothing else differs but the order.
        assertThat(UsHomeHeaderCorner - UsHeaderCornerAction.MESSAGES).containsExactlyElementsIn(UsHeaderCorner)
    }

    @Test
    fun `the bell is on every header`() {
        assertThat(UsHeaderCorner).contains(UsHeaderCornerAction.NOTIFICATIONS)
        assertThat(UsHomeHeaderCorner).contains(UsHeaderCornerAction.NOTIFICATIONS)
    }

    @Test
    fun `no glyph is drawn twice`() {
        assertThat(UsHeaderCorner).containsNoDuplicates()
        assertThat(UsHomeHeaderCorner).containsNoDuplicates()
    }

    @Test
    fun `More is the three dots, Search the magnifier and Notifications the bell`() {
        assertThat(UsHeaderCornerAction.MORE.icon).isSameInstanceAs(UsIcons.More)
        assertThat(UsHeaderCornerAction.MORE.icon).isNotSameInstanceAs(UsIcons.Menu)
        assertThat(UsHeaderCornerAction.SEARCH.icon).isSameInstanceAs(UsIcons.Search)
        assertThat(UsHeaderCornerAction.NOTIFICATIONS.icon).isSameInstanceAs(UsIcons.Notifications)
        assertThat(UsHeaderCornerAction.MESSAGES.icon).isSameInstanceAs(UsIcons.Comment)
    }

    @Test
    fun `there is no create button in the header - Create lives in the bottom bar`() {
        assertThat(UsHeaderCornerAction.entries.map { it.icon }).doesNotContain(UsIcons.Create)
        assertThat(UsHeaderCornerAction.entries.map { it.description.lowercase() }).containsNoneOf("create", "new")
    }

    @Test
    fun `each glyph says what it is to a screen reader`() {
        assertThat(UsHeaderCornerAction.SEARCH.description).isEqualTo("Search")
        assertThat(UsHeaderCornerAction.MORE.description).isEqualTo("More")
        assertThat(UsHeaderCornerAction.NOTIFICATIONS.description).isEqualTo("Notifications")
        assertThat(UsHeaderCornerAction.MESSAGES.description).isEqualTo("Messages")
    }

    // ── The screens, read as source ─────────────────────────────────────
    //
    // A composable's glyph order cannot be asserted on the JVM, so the rule is held the way
    // `NoRawColourGuardTest` holds its own: the headers' sources are read.

    private val root: File by lazy {
        var dir = File("").absoluteFile
        while (dir.name != "android" && dir.parentFile != null) dir = dir.parentFile
        check(dir.name == "android") { "could not locate the Android tree from ${File("").absolutePath}" }
        dir
    }

    private fun source(path: String): String = File(root, path).readText()

    private val homeHeader = "feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/MomentumHeader.kt"
    private val spec = "core/designsystem/src/main/kotlin/com/us/android/core/designsystem/component/"

    @Test
    fun `Home's header asks for Home's corner`() {
        assertThat(source(homeHeader)).contains("corner = UsHomeHeaderCorner")
    }

    @Test
    fun `no screen but Home asks for Home's corner`() {
        val asking = root.walkTopDown()
            .onEnter { it.name != "build" && it.name != ".gradle" }
            .filter { it.isFile && it.extension == "kt" }
            .map { it.relativeTo(root).path.replace('\\', '/') to it }
            .filter { (path, file) -> "/src/main/" in path && "UsHomeHeaderCorner" in file.readText() }
            .map { it.first }
            .toList()

        // The spec itself, the shared header (its doc and preview), and Home.
        assertThat(asking).containsExactly(spec + "UsHeaderCorner.kt", spec + "UsTopBar.kt", homeHeader)
    }

    @Test
    fun `the shared header draws the standard corner unless told otherwise`() {
        val header = source(spec + "UsTopBar.kt")

        assertThat(header).contains("corner: List<UsHeaderCornerAction> = UsHeaderCorner,")
        assertThat(header).contains("corner.forEach { action ->")
    }

    @Test
    fun `Reels and Tube draw their corners by walking the standard one`() {
        val headers = listOf(
            "feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/reels/ReelsScreen.kt",
            "feature/tube/src/main/kotlin/com/us/android/feature/tube/ui/TubeChrome.kt",
        )
        for (path in headers) {
            assertThat(source(path)).contains("UsHeaderCorner.forEach { action ->")
        }
    }

    @Test
    fun `the Me tab wears the standard corner with a menu behind More`() {
        val me = source("feature/profile/src/main/kotlin/com/us/android/feature/profile/ui/ProfileScreen.kt")
        val header = me.substringAfter("private fun OwnProfileHeader(").substringBefore("\n}")

        assertThat(header).contains("UsMomentumHeader(")
        assertThat(header).doesNotContain("corner =")
        assertThat(header).doesNotContain("onMessages =")
        assertThat(header).contains("onMore = { menuOpen = true }")
    }

    @Test
    fun `the bell says how many are unread, as one sentence`() {
        assertThat(usNotificationsDescription(0)).isEqualTo("Notifications")
        assertThat(usNotificationsDescription(-1)).isEqualTo("Notifications")
        assertThat(usNotificationsDescription(1)).isEqualTo("Notifications, 1 unread")
        assertThat(usNotificationsDescription(12)).isEqualTo("Notifications, 12 unread")
    }
}
