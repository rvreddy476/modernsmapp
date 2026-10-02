package com.us.android.feature.live.data

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * Protects the chat's own rules (2026-10-02): a row is labelled by its
 * author's NAME and never by a piece of the user id; host, moderators and
 * founding creators are marked; the three comments over the video are the
 * latest three, oldest first, minus anything removed; and a draft is counted
 * and cut the way the server counts — by code points, never through an emoji.
 */
class LiveChatTest {

    private fun message(id: String, userId: String = "u-$id", author: LiveChatAuthorDto? = null) =
        LiveChatMessageDto(id = id, userId = userId, text = "text $id", author = author)

    // ── Who wrote it ────────────────────────────────────────────────────

    @Test
    fun `the name is author name, then the handle, then Viewer`() {
        val table = listOf(
            LiveChatAuthorDto(userId = "5f0c2a9e", name = "Asha Rao", handle = "asha") to "Asha Rao",
            LiveChatAuthorDto(userId = "5f0c2a9e", name = "  Asha Rao ") to "Asha Rao",
            LiveChatAuthorDto(userId = "5f0c2a9e", name = "", handle = "asha") to "@asha",
            LiveChatAuthorDto(userId = "5f0c2a9e", name = "   ", handle = "@asha") to "@asha",
            LiveChatAuthorDto(userId = "5f0c2a9e") to "Viewer",
            LiveChatAuthorDto(userId = "5f0c2a9e", name = "", handle = "  ") to "Viewer",
            null to "Viewer",
        )

        for ((author, expected) in table) {
            assertThat(chatAuthorName(author)).isEqualTo(expected)
        }
    }

    @Test
    fun `no label ever carries a piece of the user id`() {
        val id = "5f0c2a9e-1111-2222"
        val labels = listOf(
            chatAuthorName(null),
            chatAuthorName(LiveChatAuthorDto(userId = id)),
            chatAuthorName(LiveChatAuthorDto(userId = id, role = "viewer")),
        )

        for (label in labels) {
            assertThat(label).doesNotContain("5f0c2a")
            assertThat(label).isEqualTo(CHAT_AUTHOR_FALLBACK)
        }
    }

    @Test
    fun `the role is the row's own, else what the screen knows of the host and the moderators`() {
        fun row(role: String, userId: String = "u1") =
            message("m", userId = userId, author = LiveChatAuthorDto(userId = userId, role = role))

        assertThat(chatRoleOf(row("host"), hostId = "", moderators = emptyList())).isEqualTo(ChatRole.Host)
        assertThat(chatRoleOf(row(" Moderator "), hostId = "", moderators = emptyList()))
            .isEqualTo(ChatRole.Moderator)
        assertThat(chatRoleOf(row("viewer"), hostId = "h", moderators = listOf("m"))).isEqualTo(ChatRole.Viewer)
        // No author on the row (an older server): the stream's own facts.
        assertThat(chatRoleOf(message("m", userId = "h"), hostId = "h", moderators = emptyList()))
            .isEqualTo(ChatRole.Host)
        assertThat(chatRoleOf(message("m", userId = "mod"), hostId = "h", moderators = listOf("mod")))
            .isEqualTo(ChatRole.Moderator)
        assertThat(chatRoleOf(message("m", userId = "x"), hostId = "h", moderators = listOf("mod")))
            .isEqualTo(ChatRole.Viewer)
        // A blank id never matches a blank host id.
        assertThat(chatRoleOf(message("m", userId = ""), hostId = "", moderators = listOf("")))
            .isEqualTo(ChatRole.Viewer)
    }

    @Test
    fun `the founding creator badge is shown only for its token`() {
        assertThat(isFoundingCreator(LiveChatAuthorDto(badges = listOf("founding_creator")))).isTrue()
        assertThat(isFoundingCreator(LiveChatAuthorDto(badges = listOf("verified", " Founding_Creator ")))).isTrue()
        assertThat(isFoundingCreator(LiveChatAuthorDto(badges = listOf("verified")))).isFalse()
        assertThat(isFoundingCreator(LiveChatAuthorDto())).isFalse()
        assertThat(isFoundingCreator(null)).isFalse()
    }

    @Test
    fun `people are remembered by user id, and a later row replaces an earlier one`() {
        val first = chatPeople(
            emptyMap(),
            listOf(
                message("1", userId = "a", author = LiveChatAuthorDto(userId = "a", name = "Asha")),
                message("2", userId = "b"),
                message("3", userId = "", author = LiveChatAuthorDto(userId = "c", name = "Chitra")),
                message("4", userId = "", author = LiveChatAuthorDto(name = "Nobody")),
            ),
        )
        assertThat(first.mapValues { it.value.name }).containsExactly("a", "Asha", "c", "Chitra")

        val second = chatPeople(
            first,
            listOf(message("5", userId = "a", author = LiveChatAuthorDto(userId = "a", name = "Asha R"))),
        )
        assertThat(second.mapValues { it.value.name }).containsExactly("a", "Asha R", "c", "Chitra")
        // A page with no authors forgets nobody.
        assertThat(chatPeople(second, listOf(message("6", userId = "z")))).isEqualTo(second)
    }

    // ── Over the video ──────────────────────────────────────────────────

    /** `GET …/chat` is newest first. */
    private val newestFirst = listOf(message("5"), message("4"), message("3"), message("2"), message("1"))

    @Test
    fun `the overlay is the latest three, oldest first so the newest is at the bottom`() {
        val log = ChatLog().withSnapshot(newestFirst)

        assertThat(log.overlayComments().map { it.id }).containsExactly("3", "4", "5").inOrder()
    }

    @Test
    fun `fewer than three comments are all shown, and none is none`() {
        assertThat(ChatLog().overlayComments()).isEmpty()
        assertThat(ChatLog().withSnapshot(listOf(message("2"), message("1"))).overlayComments().map { it.id })
            .containsExactly("1", "2").inOrder()
        assertThat(ChatLog().withSnapshot(newestFirst).overlayComments(limit = 0)).isEmpty()
    }

    @Test
    fun `a removed message leaves the overlay and the next one down takes its place`() {
        val log = ChatLog().withSnapshot(newestFirst).withRemoved("4")

        assertThat(log.overlayComments().map { it.id }).containsExactly("2", "3", "5").inOrder()
        // A poll that still carries it cannot bring it back over the video.
        assertThat(log.withSnapshot(newestFirst).overlayComments().map { it.id })
            .containsExactly("2", "3", "5").inOrder()
    }

    @Test
    fun `a message missing from the next poll leaves the overlay too`() {
        val log = ChatLog().withSnapshot(newestFirst).withSnapshot(newestFirst.filterNot { it.id == "5" })

        assertThat(log.overlayComments().map { it.id }).containsExactly("2", "3", "4").inOrder()
    }

    @Test
    fun `the viewer's own sent message is the newest on the overlay at once`() {
        val log = ChatLog().withSnapshot(newestFirst).withSent(message("6"))

        assertThat(log.overlayComments().map { it.id }).containsExactly("4", "5", "6").inOrder()
    }

    // ── What a draft may hold ───────────────────────────────────────────

    @Test
    fun `length is counted in code points, as the server counts runes`() {
        assertThat("hello".codePointLength()).isEqualTo(5)
        assertThat("😀".length).isEqualTo(2)
        assertThat("😀".codePointLength()).isEqualTo(1)
        assertThat("a😀b".codePointLength()).isEqualTo(3)
        assertThat("".codePointLength()).isEqualTo(0)
    }

    @Test
    fun `500 emoji can be sent - counting UTF-16 units would refuse them`() {
        val text = "😀".repeat(MAX_CHAT_CODE_POINTS)

        assertThat(text.length).isEqualTo(1_000)
        assertThat(canSendChat(text)).isTrue()
        assertThat(clampChatDraft(text)).isEqualTo(text)
        assertThat(canSendChat(text + "😀")).isFalse()
    }

    @Test
    fun `emoji alone is something to send, blank is not`() {
        assertThat(canSendChat("🎉")).isTrue()
        assertThat(canSendChat("  ❤️  ")).isTrue()
        assertThat(canSendChat("")).isFalse()
        assertThat(canSendChat("   \n ")).isFalse()
        // Trailing spaces do not count against the limit: the send trims them.
        assertThat(canSendChat("a".repeat(MAX_CHAT_CODE_POINTS) + "   ")).isTrue()
    }

    @Test
    fun `a cut never leaves half a surrogate pair`() {
        val cut = clampToCodePoints("ab😀😀", max = 3)

        assertThat(cut).isEqualTo("ab😀")
        assertThat(cut.last().isLowSurrogate()).isTrue()
        assertThat(Character.isHighSurrogate(cut[cut.length - 2])).isTrue()
        // What String.take would have done: kept a lone high surrogate.
        assertThat("ab😀😀".take(3).last().isHighSurrogate()).isTrue()
    }

    @Test
    fun `a cut never splits an emoji made of several code points`() {
        val thumbsDark = "👍🏿" // thumbs up + skin tone
        val heart = "❤️" // heart + variation selector
        val family = "👩‍👩‍👧" // three people joined by ZWJ
        val india = "🇮🇳" // two regional indicators
        val keycap = "1️⃣" // digit + variation selector + keycap

        assertThat(clampToCodePoints("a$thumbsDark", max = 2)).isEqualTo("a")
        assertThat(clampToCodePoints("a$heart", max = 2)).isEqualTo("a")
        assertThat(clampToCodePoints("a$family", max = 2)).isEqualTo("a")
        assertThat(clampToCodePoints("a$family", max = 3)).isEqualTo("a")
        assertThat(clampToCodePoints("a$family", max = 5)).isEqualTo("a")
        assertThat(clampToCodePoints("a$family", max = 6)).isEqualTo("a$family")
        assertThat(clampToCodePoints("a$india", max = 2)).isEqualTo("a")
        assertThat(clampToCodePoints("$india$india", max = 3)).isEqualTo(india)
        assertThat(clampToCodePoints("a$keycap", max = 3)).isEqualTo("a")
        assertThat(clampToCodePoints("éx", max = 1)).isEmpty()
        assertThat(clampToCodePoints("éx", max = 2)).isEqualTo("é")
    }

    @Test
    fun `a cut leaves short text alone and handles the edges`() {
        assertThat(clampToCodePoints("hello", max = 5)).isEqualTo("hello")
        assertThat(clampToCodePoints("hello", max = 3)).isEqualTo("hel")
        assertThat(clampToCodePoints("hello", max = 0)).isEmpty()
        assertThat(clampToCodePoints("", max = 5)).isEmpty()
        assertThat(clampToCodePoints("😀😀😀", max = 2)).isEqualTo("😀😀")
    }
}
