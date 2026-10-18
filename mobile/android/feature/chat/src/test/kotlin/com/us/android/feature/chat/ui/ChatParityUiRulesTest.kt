package com.us.android.feature.chat.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.MediaInfo
import com.us.android.core.chat.data.Message
import com.us.android.feature.chat.ui.home.activityVisible
import com.us.android.feature.chat.ui.home.labelForStatus
import org.junit.Test

/** Chat parity (2026-10-18): the thread's and inbox's pure rules. */
class ChatParityUiRulesTest {

    private fun msg(
        id: String = "m1",
        sender: String = "me",
        text: String = "hi",
        media: MediaInfo? = null,
        deleted: Boolean = false,
    ) = Message(
        id = id, conversationId = "c", senderId = sender, senderDisplayName = null, text = text,
        createdAt = "t", bucket = "202610", ts = "t", media = media, deleted = deleted,
        mediaId = if (media != null) "media-1" else null,
    )

    private fun row(id: String, type: String = "direct", request: Boolean = false, source: String? = null) = Conversation(
        id = id, type = type, title = id, isRequest = request, members = emptyList(), updatedAt = "2026-10-18T00:00:00Z",
        sourceApp = source,
    )

    @Test
    fun `sensitive media blurs when asked and always in a request`() {
        val sensitive = msg(media = MediaInfo("image", 1, 1, null, sensitive = true))
        assertThat(shouldBlur(sensitive, sensitiveFilterOn = true, isRequest = false, revealed = false)).isTrue()
        assertThat(shouldBlur(sensitive, sensitiveFilterOn = false, isRequest = true, revealed = false)).isTrue()
        assertThat(shouldBlur(sensitive, sensitiveFilterOn = false, isRequest = false, revealed = false)).isFalse()
        assertThat(shouldBlur(sensitive, sensitiveFilterOn = true, isRequest = true, revealed = true)).isFalse()
        // Unknown (null) is not sensitive: the server sends null for now.
        val unknown = msg(media = MediaInfo("image", 1, 1, null, sensitive = null))
        assertThat(shouldBlur(unknown, sensitiveFilterOn = true, isRequest = true, revealed = false)).isFalse()
    }

    @Test
    fun `only the viewer's own plain text can be edited`() {
        assertThat(msg().canEdit("me")).isTrue()
        assertThat(msg(sender = "them").canEdit("me")).isFalse()
        assertThat(msg(deleted = true).canEdit("me")).isFalse()
        assertThat(msg(media = MediaInfo("image", 1, 1, null, null)).canEdit("me")).isFalse()
        assertThat(msg().copy(type = "voice").canEdit("me")).isFalse()
        assertThat(msg().canEdit("")).isFalse()
    }

    @Test
    fun `an unsent or pending message cannot be forwarded`() {
        assertThat(msg().canForward()).isTrue()
        assertThat(msg(deleted = true).canForward()).isFalse()
        assertThat(msg().copy(pending = true).canForward()).isFalse()
        assertThat(msg(text = "").canForward()).isFalse()
    }

    @Test
    fun `forward targets skip this chat, requests, Pulse chats and broadcast channels`() {
        val targets = forwardTargets(
            listOf(
                row("this"),
                row("dm"),
                row("group", type = "group"),
                row("req", request = true),
                row("pulse", source = "dating"),
                row("channel", type = "broadcast"),
            ),
            currentId = "this",
        )
        assertThat(targets.map { it.id }).containsExactly("dm", "group")
    }

    @Test
    fun `the shared viewer starts at the tapped post, or puts it first`() {
        assertThat(sharedReelOrder(listOf("p3", "p2", "p1"), "p2")).isEqualTo(listOf("p3", "p2", "p1") to 1)
        assertThat(sharedReelOrder(listOf("p3", "p3", "", "p1"), "p1")).isEqualTo(listOf("p3", "p1") to 1)
        assertThat(sharedReelOrder(listOf("p3"), "p9")).isEqualTo(listOf("p9", "p3") to 0)
        assertThat(sharedReelOrder(emptyList(), "p9")).isEqualTo(listOf("p9") to 0)
    }

    @Test
    fun `a Giphy pick keeps the provider's url and needs an id and a url`() {
        val gif = giphyGifOf("g1", "https://media.giphy.com/x.gif?cid=1&ct=g", 480, 270, "https://media.giphy.com/s.gif")
        assertThat(gif?.provider).isEqualTo("giphy")
        assertThat(gif?.url).isEqualTo("https://media.giphy.com/x.gif?cid=1&ct=g")
        assertThat(gif?.stillUrl).isEqualTo("https://media.giphy.com/s.gif")
        assertThat(giphyGifOf("", "u", 1, 1, null)).isNull()
        assertThat(giphyGifOf("g", null, 1, 1, null)).isNull()
        assertThat(giphyGifOf("g", "u", 1, 1, " ")?.stillUrl).isNull()
    }

    @Test
    fun `the people picker caps a new group and toggles`() {
        val a = PickablePerson("a", "A", "", null)
        val b = PickablePerson("b", "B", "", null)
        assertThat(emptyList<PickablePerson>().toggled(a).toggled(b).map { it.userId }).containsExactly("a", "b").inOrder()
        assertThat(listOf(a, b).toggled(a).map { it.userId }).containsExactly("b")
        assertThat(listOf(a).toggled(b, cap = 1)).containsExactly(a)
    }

    @Test
    fun `a typed last message says what it was`() {
        assertThat(typedRowPreview("voice")).isEqualTo("Voice message")
        assertThat(typedRowPreview("call")).isEqualTo("Call")
        assertThat(typedRowPreview("media")).isEqualTo("Photo")
    }

    @Test
    fun `the activity dot is lit unless no one can see it`() {
        assertThat(activityVisible("everyone")).isTrue()
        assertThat(activityVisible("connections_only")).isTrue()
        assertThat(activityVisible("no_one")).isFalse()
        assertThat(activityVisible(null)).isFalse()
        assertThat(labelForStatus("no_one")).isEqualTo("No one")
    }

    // ── fix pass 2 ──────────────────────────────────────────────────────

    private fun pack(id: String, name: String, stickers: Int) = com.us.android.core.chat.data.StickerPack(
        id = id,
        name = name,
        stickers = (1..stickers).map { com.us.android.core.chat.data.StickerInfo("$id-$it", "https://cdn/$id/$it.webp", 1, 1) },
    )

    @Test
    fun `the panel shows Giphy Stickers beside GIF, then our non-empty packs as their own tabs`() {
        val packs = listOf(pack("p1", "Basics", 2), pack("p2", "Empty", 0), pack("p3", " ", 1))
        val withGiphy = composerPanelTabs(giphyAvailable = true, packs = packs)
        assertThat(withGiphy.map { it.label }).containsExactly("Emoji", "Stickers", "GIF", "Basics", "Pack").inOrder()
        assertThat(withGiphy.map { it.tag }).containsExactly("emoji", "stickers", "gif", "pack:p1", "pack:p3").inOrder()
        // No Giphy key in this build: neither Giphy tab; our packs still show.
        assertThat(composerPanelTabs(giphyAvailable = false, packs = packs).map { it.label })
            .containsExactly("Emoji", "Basics", "Pack").inOrder()
        assertThat(composerPanelTabs(giphyAvailable = false, packs = emptyList())).containsExactly(PanelTab.Emoji)
    }

    @Test
    fun `a Giphy sticker pick is marked as one`() {
        assertThat(giphyGifOf("s1", "https://media.giphy.com/s.gif", 200, 200, null, isSticker = true)?.isSticker).isTrue()
        assertThat(giphyGifOf("g1", "https://media.giphy.com/g.gif", 200, 200, null)?.isSticker).isFalse()
    }

    @Test
    fun `a released swipe opens past half or on a fling, and shuts otherwise`() {
        val start = 76f
        val end = 152f
        assertThat(swipeRestingOffset(50f, 0f, start, end)).isEqualTo(start)
        assertThat(swipeRestingOffset(20f, 0f, start, end)).isEqualTo(0f)
        assertThat(swipeRestingOffset(20f, 3_000f, start, end)).isEqualTo(start)
        assertThat(swipeRestingOffset(-90f, 0f, start, end)).isEqualTo(-end)
        assertThat(swipeRestingOffset(-40f, 0f, start, end)).isEqualTo(0f)
        assertThat(swipeRestingOffset(-40f, -3_000f, start, end)).isEqualTo(-end)
        // A fling the other way shuts it; a side with no buttons never opens.
        assertThat(swipeRestingOffset(-40f, 3_000f, start, end)).isEqualTo(0f)
        assertThat(swipeRestingOffset(60f, 0f, 0f, end)).isEqualTo(0f)
    }

    @Test
    fun `chat report uses the conversation route and falls back only when it is missing`() = kotlinx.coroutines.test.runTest {
        var oldPathCalls = 0
        val oldPath: suspend () -> com.us.android.core.common.result.AppResult<Unit>? = {
            oldPathCalls++
            com.us.android.core.common.result.AppResult.Success(Unit)
        }
        val ok = reportChat(whole = { com.us.android.core.common.result.AppResult.Success(Unit) }, oldPath = oldPath)
        assertThat(ok).isInstanceOf(com.us.android.core.common.result.AppResult.Success::class.java)
        assertThat(oldPathCalls).isEqualTo(0)

        val refused = com.us.android.core.common.result.AppResult.Failure(
            com.us.android.core.common.error.AppError.Forbidden(code = "NOT_A_MEMBER"),
        )
        assertThat(reportChat(whole = { refused }, oldPath = oldPath)).isEqualTo(refused)
        assertThat(oldPathCalls).isEqualTo(0)

        val missing = com.us.android.core.common.result.AppResult.Failure(com.us.android.core.common.error.AppError.NotFound())
        assertThat(reportChat(whole = { missing }, oldPath = oldPath))
            .isInstanceOf(com.us.android.core.common.result.AppResult.Success::class.java)
        assertThat(oldPathCalls).isEqualTo(1)
    }
}
