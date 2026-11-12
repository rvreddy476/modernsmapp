package com.us.android.feature.echo

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.echo.data.EchoMediaUploader
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.MEDIA_LIMIT_MESSAGE
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.ComposerViewModel
import io.mockk.mockk
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset

/**
 * The composer: the eligibility backstop, the 500-character and media
 * limits, threads of 2 to 25, replies and quotes, the 30-minute edit, and
 * one Idempotency-Key per submitted draft.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ComposerViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeEchoRepository()
    private val media = mockk<EchoMediaUrls>(relaxed = true)

    /** Uploads answer `m-<uri>`; a uri containing "gif" is a GIF; one containing "bad" fails. */
    private val uploader = object : EchoMediaUploader {
        override fun isGif(uri: String) = "gif" in uri
        override suspend fun upload(uri: String): AppResult<String> =
            if ("bad" in uri) AppResult.Failure(AppError.Unknown(null, null)) else AppResult.Success("m-$uri")
    }

    private var now = Instant.parse("2026-11-12T09:40:00Z")
    private val clock get() = Clock.fixed(now, ZoneOffset.UTC)

    private fun model(mode: ComposeMode = ComposeMode.NEW, target: String = "") = ComposerViewModel(
        SavedStateHandle(mapOf(ComposerViewModel.MODE_KEY to mode.wire, ComposerViewModel.TARGET_KEY to target)),
        repo,
        uploader,
        media,
        FakeSession("me"),
        clock,
    )

    @Test
    fun `a viewer without Premium gets the sheet and nothing can be sent`() = runTest(dispatcher) {
        repo.eligibility = AppResult.Success(NOT_PREMIUM)
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "hello")
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.PREMIUM_REQUIRED)
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.posts).isEmpty()
    }

    @Test
    fun `a failed eligibility read fails closed`() = runTest(dispatcher) {
        repo.eligibility = AppResult.Failure(AppError.NoNetwork())
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "hello")
        assertThat(vm.state.value.blocked).isNotNull()
        assertThat(vm.state.value.canSubmit).isFalse()
    }

    @Test
    fun `500 characters is the limit`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "a".repeat(500))
        assertThat(vm.state.value.canSubmit).isTrue()
        assertThat(vm.state.value.entries[0].remaining).isEqualTo(0)
        vm.setText(0, "a".repeat(501))
        assertThat(vm.state.value.canSubmit).isFalse()
        assertThat(vm.state.value.problem).isEqualTo("Notes can be up to 500 characters.")
        assertThat(vm.state.value.entries[0].remaining).isEqualTo(-1)
    }

    @Test
    fun `four photos at most, a GIF alone, extra picks left out with a notice`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.addMedia(0, listOf("p1", "p2", "p3", "p4", "p5"))
        advanceUntilIdle()
        assertThat(
            vm.state.value.entries[0].media.map { it.mediaId }
        ).containsExactly("m-p1", "m-p2", "m-p3", "m-p4").inOrder()
        assertThat(vm.state.value.notice).isEqualTo(MEDIA_LIMIT_MESSAGE)

        val gif = model()
        advanceUntilIdle()
        gif.addMedia(0, listOf("a.gif"))
        gif.addMedia(0, listOf("p1"))
        advanceUntilIdle()
        assertThat(gif.state.value.entries[0].media.map { it.localKey }).containsExactly("a.gif")
    }

    @Test
    fun `a single note posts its text, media and who can reply`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, " hi ")
        vm.addMedia(0, listOf("p1"))
        vm.setReplyPolicy(ReplyPolicy.FOLLOWING)
        advanceUntilIdle()
        vm.submit()
        advanceUntilIdle()
        val sent = repo.posts.single()
        assertThat(sent.text).isEqualTo(" hi ")
        assertThat(sent.mediaIds).containsExactly("m-p1")
        assertThat(sent.policy).isEqualTo(ReplyPolicy.FOLLOWING)
        assertThat(sent.replyToId).isNull()
        assertThat(sent.quoteOfId).isNull()
        assertThat(sent.key).isNotEmpty()
        assertThat(vm.state.value.doneNoteId).isEqualTo("posted")
    }

    @Test
    fun `a photo still uploading or failed holds Post`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "hi")
        vm.addMedia(0, listOf("bad"))
        advanceUntilIdle()
        assertThat(vm.state.value.canSubmit).isFalse()
        vm.removeMedia(0, "bad")
        assertThat(vm.state.value.canSubmit).isTrue()
    }

    @Test
    fun `a retry of the same draft sends the same key, a changed draft a new one`() = runTest(dispatcher) {
        repo.postResult = AppResult.Failure(AppError.Timeout())
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "hi")
        vm.submit()
        advanceUntilIdle()
        assertThat(vm.state.value.error).isEqualTo(EchoWords.NO_NETWORK)
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.posts.map { it.key }.distinct()).hasSize(1)

        vm.setText(0, "hi there")
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.posts.map { it.key }.distinct()).hasSize(2)
    }

    @Test
    fun `threads add notes up to 25 and post through the thread route`() = runTest(dispatcher) {
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "one")
        repeat(30) { vm.addEntry() }
        assertThat(vm.state.value.entries).hasSize(25)
        assertThat(vm.state.value.canAddEntry).isFalse()
        // Empty entries hold Post.
        assertThat(vm.state.value.canSubmit).isFalse()
        repeat(23) { vm.removeEntry(2) }
        assertThat(vm.state.value.entries).hasSize(2)
        vm.setText(1, "two")
        assertThat(vm.state.value.isThread).isTrue()
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.posts).isEmpty()
        assertThat(repo.threads.single().second.map { it.first }).containsExactly("one", "two").inOrder()
        assertThat(vm.state.value.doneNoteId).isEqualTo("t1")
    }

    @Test
    fun `a reply checks can_reply and the note's own policy`() = runTest(dispatcher) {
        repo.notes["n1"] = AppResult.Success(note("n1", canReply = false, policy = ReplyPolicy.MENTIONED))
        repo.eligibility = AppResult.Success(NOT_PREMIUM)
        val closed = model(ComposeMode.REPLY, "n1")
        advanceUntilIdle()
        assertThat(closed.state.value.blocked?.detail).isEqualTo(EchoWords.REPLY_NOT_ALLOWED)

        repo.notes["n2"] = AppResult.Success(note("n2"))
        val open = model(ComposeMode.REPLY, "n2")
        advanceUntilIdle()
        // Premium is not needed to reply (decision 6).
        assertThat(open.state.value.blocked).isNull()
        assertThat(open.state.value.showsReplyPolicy).isFalse()
        assertThat(open.state.value.canAddEntry).isFalse()
        open.setText(0, "agreed")
        open.submit()
        advanceUntilIdle()
        assertThat(repo.posts.single().replyToId).isEqualTo("n2")
        assertThat(repo.posts.single().policy).isNull()

        repo.eligibility = AppResult.Success(TOO_NEW)
        val young = model(ComposeMode.REPLY, "n2")
        advanceUntilIdle()
        assertThat(young.state.value.blocked?.title).isEqualTo(EchoWords.ACCOUNT_TOO_NEW)
    }

    @Test
    fun `a quote needs Premium and sends quote_of_id`() = runTest(dispatcher) {
        repo.notes["q"] = AppResult.Success(note("q"))
        val vm = model(ComposeMode.QUOTE, "q")
        advanceUntilIdle()
        assertThat(vm.state.value.target?.id).isEqualTo("q")
        vm.setText(0, "look")
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.posts.single().quoteOfId).isEqualTo("q")
        assertThat(repo.posts.single().replyToId).isNull()
    }

    @Test
    fun `the server's premium refusal turns into the sheet`() = runTest(dispatcher) {
        repo.postResult = AppResult.Failure(AppError.Forbidden("PREMIUM_REQUIRED"))
        val vm = model()
        advanceUntilIdle()
        vm.setText(0, "hi")
        vm.submit()
        advanceUntilIdle()
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.PREMIUM_REQUIRED)
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `an edit within 30 minutes sends the new text, after it only who can reply`() = runTest(dispatcher) {
        val mine = note("mine", authorId = "me", createdAt = "2026-11-12T09:30:00Z", text = "old")
        repo.notes["mine"] = AppResult.Success(mine)
        val vm = model(ComposeMode.EDIT, "mine")
        advanceUntilIdle()
        assertThat(repo.eligibilityReads).isEqualTo(0)
        assertThat(vm.state.value.textLocked).isFalse()
        assertThat(vm.state.value.entries[0].text).isEqualTo("old")
        assertThat(vm.state.value.canAddMedia).isFalse()
        vm.setText(0, "new")
        vm.submit()
        advanceUntilIdle()
        assertThat(repo.edits.single()).isEqualTo(Triple("mine", "new", null))

        now = Instant.parse("2026-11-12T10:00:00Z")
        val late = model(ComposeMode.EDIT, "mine")
        advanceUntilIdle()
        assertThat(late.state.value.textLocked).isTrue()
        late.setText(0, "newer")
        assertThat(late.state.value.entries[0].text).isEqualTo("old")
        late.setReplyPolicy(ReplyPolicy.MENTIONED)
        late.submit()
        advanceUntilIdle()
        assertThat(repo.edits.last()).isEqualTo(Triple("mine", null, ReplyPolicy.MENTIONED))
    }

    @Test
    fun `a missing target closes the composer`() = runTest(dispatcher) {
        val vm = model(ComposeMode.QUOTE, "gone")
        advanceUntilIdle()
        assertThat(vm.state.value.blocked?.detail).isEqualTo(EchoWords.NOTE_NOT_FOUND)
        assertThat(vm.state.value.canSubmit).isFalse()
    }
}
