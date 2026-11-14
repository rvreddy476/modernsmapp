package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NoteAuthor
import com.us.android.feature.echo.data.VerificationMark
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.EchoNoteActions
import com.us.android.feature.echo.ui.ReplyPreviewStore
import com.us.android.feature.echo.ui.TopicLine
import com.us.android.feature.echo.ui.charactersLeft
import com.us.android.feature.echo.ui.continuesThread
import com.us.android.feature.echo.ui.countWords
import com.us.android.feature.echo.ui.firstHashtag
import com.us.android.feature.echo.ui.isLongNote
import com.us.android.feature.echo.ui.placeholder
import com.us.android.feature.echo.ui.repliesLabel
import com.us.android.feature.echo.ui.replyPreview
import com.us.android.feature.echo.ui.replyPrompt
import com.us.android.feature.echo.ui.replyRows
import com.us.android.feature.echo.ui.submitLabel
import com.us.android.feature.echo.ui.threadPartsEndingAt
import com.us.android.feature.echo.ui.topicLine
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * Echo look v3 (docs/designs/echo-look-v3.md): the topic line on top of a
 * note, the reply bubbles on a timeline card and how their replies are read,
 * a reply's indent on the note page, the thread line, and the composer's words.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class EchoLookTest {

    private fun author(username: String, name: String = username.replaceFirstChar { it.uppercase() }) =
        NoteAuthor(
            id = username,
            username = username,
            displayName = name,
            avatarUrl = null,
            mark = VerificationMark.NONE,
        )

    private fun Note.by(username: String) = copy(authorId = username, author = author(username))

    // ── the topic line ──

    @Test
    fun `the first hashtag leads the topic line in the accent`() {
        val line = topicLine(note("n", text = "Monsoon is late #weather and #mumbai"), age = "2h")
        assertThat(line).isEqualTo(TopicLine("#weather · 2h", accent = true))
        assertThat(firstHashtag("no tags here")).isNull()
    }

    @Test
    fun `no hashtag is just the time in secondary text`() {
        assertThat(topicLine(note("n", text = "Plain words"), age = "2h")).isEqualTo(TopicLine("2h", accent = false))
    }

    @Test
    fun `a quote, a reply and a thread name themselves`() {
        val quote = note("q", text = "So true #x").copy(quoteOfId = "orig")
        assertThat(topicLine(quote, "2h")).isEqualTo(TopicLine("Quote · 2h", accent = true))

        val reply = note("r", replyToId = "p")
        assertThat(topicLine(reply, "2h", replyToHandle = "@ada").text).isEqualTo("Reply to @ada · 2h")
        assertThat(topicLine(reply, "2h").text).isEqualTo("Reply · 2h")

        // A thread part is a reply to the part before it: the thread wins.
        assertThat(topicLine(reply, "5h", thread = true, threadParts = 3).text).isEqualTo("Thread · 3 parts · 5h")
        assertThat(topicLine(note("t"), "5h", thread = true).text).isEqualTo("Thread · 5h")
    }

    @Test
    fun `an edited note says so on its topic line`() {
        val edited = note("e", text = "#news update").copy(editedAt = "2026-11-12T09:35:00Z")
        assertThat(topicLine(edited, "1m").text).isEqualTo("#news · 1m · Edited")
    }

    @Test
    fun `a note past 280 characters is a long note`() {
        assertThat(isLongNote("a".repeat(280))).isFalse()
        assertThat(isLongNote("a".repeat(281))).isTrue()
    }

    // ── the reply bubbles ──

    @Test
    fun `no bubbles until the replies arrive, and none when there are none`() {
        val parent = note("p").copy(replyCount = 4)
        assertThat(replyPreview(parent, replies = null)).isNull()
        assertThat(replyPreview(note("p"), replies = emptyList())).isNull()
    }

    @Test
    fun `at most two bubbles, and N replies from the note's count`() {
        val parent = note("p").copy(replyCount = 12)
        val replies = listOf("a", "b", "c").map { note(it, replyToId = "p").by(it) }
        val preview = replyPreview(parent, replies)!!
        assertThat(preview.bubbles.map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(preview.label).isEqualTo("12 replies")
        assertThat(preview.thread).isFalse()
    }

    @Test
    fun `one reply reads 1 reply`() {
        val parent = note("p").copy(replyCount = 1)
        val preview = replyPreview(parent, listOf(note("a", replyToId = "p").by("ada")))!!
        assertThat(preview.label).isEqualTo("1 reply")
        assertThat(repliesLabel(0)).isNull()
        assertThat(repliesLabel(3)).isEqualTo("3 replies")
    }

    @Test
    fun `the author continuing their own note is a thread, not a bubble`() {
        val head = note("h", authorId = "me").copy(replyCount = 2)
        val replies = listOf(note("h2", authorId = "me", replyToId = "h"), note("x", replyToId = "h").by("ada"))
        val preview = replyPreview(head, replies)!!
        assertThat(preview.thread).isTrue()
        assertThat(preview.bubbles.map { it.id }).containsExactly("x")
        assertThat(preview.label).isEqualTo("2 replies")
    }

    // ── reading the bubbles' replies ──

    private fun store(repo: FakeEchoRepository, scope: kotlinx.coroutines.test.TestScope) =
        EchoNoteActions(scope = scope, deps = testDeps(repo), apply = { _, _ -> }, remove = {}).replyPreviews

    @Test
    fun `replies are read once per card on screen, two at a time, only where there are any`() =
        runTest(UnconfinedTestDispatcher()) {
            val repo = FakeEchoRepository()
            val replies = listOf("a", "b", "c").map { note(it, replyToId = "p") }
            repo.previewResults["p"] = AppResult.Success(replies)
            val previews = store(repo, this)

            val withReplies = note("p").copy(replyCount = 3)
            previews.request(listOf(withReplies, note("quiet")))
            previews.request(listOf(withReplies))

            assertThat(repo.previewCalls).containsExactly("p" to ReplyPreviewStore.LIMIT)
            assertThat(ReplyPreviewStore.LIMIT).isEqualTo(2)
            assertThat(previews.replies.value["p"]?.map { it.id }).containsExactly("a", "b").inOrder()
            assertThat(previews.replies.value).doesNotContainKey("quiet")
        }

    @Test
    fun `a failed read shows nothing and is asked again next time`() = runTest(UnconfinedTestDispatcher()) {
        val repo = FakeEchoRepository()
        repo.previewResults["p"] = AppResult.Failure(AppError.NotFound(code = "NOTE_NOT_FOUND"))
        val previews = store(repo, this)
        val card = note("p").copy(replyCount = 1)

        previews.request(listOf(card))
        assertThat(previews.replies.value).isEmpty()

        repo.previewResults["p"] = AppResult.Success(listOf(note("a", replyToId = "p")))
        previews.request(listOf(card))
        assertThat(repo.previewCalls.map { it.first }).containsExactly("p", "p")
        assertThat(previews.replies.value["p"]?.map { it.id }).containsExactly("a")
    }

    // ── the note page ──

    @Test
    fun `replies indent one step per level, up to two, then flat with who they answer`() {
        val replies = listOf(
            note("r1", replyToId = "focus").by("ada"),
            note("r2", replyToId = "r1").by("bo"),
            note("r3", replyToId = "r2").by("cy"),
            note("r4", replyToId = "r3").by("di"),
            note("r5", replyToId = "elsewhere").by("ed"),
        )
        val rows = replyRows("focus", replies)
        assertThat(rows.map { it.depth }).containsExactly(0, 1, 2, 2, 0).inOrder()
        assertThat(rows.map { it.replyingTo }).containsExactly(null, null, null, "@cy", null).inOrder()
    }

    @Test
    fun `a thread counts its parts and joins them with the line`() {
        val one = note("1", authorId = "me")
        val two = note("2", authorId = "me", replyToId = "1")
        val three = note("3", authorId = "me", replyToId = "2")
        assertThat(threadPartsEndingAt(listOf(one, two), three)).isEqualTo(3)
        assertThat(continuesThread(one, two)).isTrue()

        // Someone else's reply breaks the thread.
        val theirs = note("x", authorId = "ada", replyToId = "2")
        assertThat(threadPartsEndingAt(listOf(one, two), theirs)).isEqualTo(1)
        assertThat(continuesThread(two, theirs)).isFalse()
        assertThat(continuesThread(two, null)).isFalse()
    }

    @Test
    fun `the reply box and the count pills use Echo's words`() {
        assertThat(replyPrompt(note("n").by("ada"))).isEqualTo("Reply to Ada…")
        assertThat(replyPrompt(note("n"))).isEqualTo("Reply…")
        assertThat(countWords(3, "echo", "echoes")).isEqualTo("3 echoes")
        assertThat(countWords(1, "heart", "hearts")).isEqualTo("1 heart")
        assertThat(countWords(0, "heart", "hearts")).isEqualTo("0 hearts")
    }

    // ── the composer ──

    @Test
    fun `the composer's words`() {
        assertThat(placeholder(ComposeMode.NEW, 0)).isEqualTo("What's worth saying today?")
        assertThat(placeholder(ComposeMode.REPLY, 0)).isEqualTo("Your reply…")
        assertThat(placeholder(ComposeMode.NEW, 1)).isEqualTo("Add another note")
        assertThat(submitLabel(ComposeMode.NEW, thread = false)).isEqualTo("Post note")
        assertThat(submitLabel(ComposeMode.REPLY, thread = false)).isEqualTo("Reply")
        assertThat(charactersLeft(500)).isEqualTo("500 left")
        assertThat(charactersLeft(-3)).isEqualTo("3 over")
    }
}
