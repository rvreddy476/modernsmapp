package com.us.android.feature.echo

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.echo.data.EchoMediaUrls
import com.us.android.feature.echo.data.EchoWords
import com.us.android.feature.echo.data.NotePage
import com.us.android.feature.echo.ui.ComposeMode
import com.us.android.feature.echo.ui.ComposeRequest
import com.us.android.feature.echo.ui.NoteDetailViewModel
import com.us.android.feature.echo.ui.NoteMenu
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

/** One note: its conversation, its replies, the reply gate, the author's menu. */
@OptIn(ExperimentalCoroutinesApi::class)
class NoteDetailViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeEchoRepository()
    private val media = mockk<EchoMediaUrls>(relaxed = true)
    private val clock = Clock.fixed(Instant.parse("2026-11-12T09:40:00Z"), ZoneOffset.UTC)

    private fun model(id: String = "n1") =
        NoteDetailViewModel(
            SavedStateHandle(mapOf(NoteDetailViewModel.NOTE_ID_KEY to id)),
            repo,
            media,
            FakeSession("me"),
            clock
        )

    @Test
    fun `a reply shows its conversation above and its replies below`() = runTest(dispatcher) {
        repo.notes["n1"] = AppResult.Success(note("n1", replyToId = "top"))
        repo.conversationResult = AppResult.Success(listOf(note("top")))
        repo.repliesPages[null] = AppResult.Success(NotePage(listOf(note("r1")), "c2"))
        repo.repliesPages["c2"] = AppResult.Success(NotePage(listOf(note("r2")), null))
        val vm = model()
        advanceUntilIdle()
        assertThat(vm.state.value.note?.id).isEqualTo("n1")
        assertThat(vm.state.value.conversation.map { it.id }).containsExactly("top")
        assertThat(vm.replyFeed.value.items.map { it.id }).containsExactly("r1")
        vm.loadMoreReplies()
        advanceUntilIdle()
        assertThat(vm.replyFeed.value.items.map { it.id }).containsExactly("r1", "r2").inOrder()
    }

    @Test
    fun `a missing note says so`() = runTest(dispatcher) {
        val vm = model("gone")
        advanceUntilIdle()
        assertThat(vm.state.value.note).isNull()
        assertThat(vm.state.value.error).isEqualTo(EchoWords.NOTE_NOT_FOUND)
    }

    @Test
    fun `reply opens the composer only when both can_reply and viewer_can_reply allow it`() = runTest(dispatcher) {
        repo.notes["n1"] = AppResult.Success(note("n1"))
        repo.eligibility = AppResult.Success(NOT_PREMIUM)
        val vm = model()
        advanceUntilIdle()
        vm.reply(vm.state.value.note!!)
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isEqualTo(ComposeRequest(ComposeMode.REPLY, "n1"))

        vm.composerOpened()
        vm.reply(note("closed", canReply = false))
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isNull()
        assertThat(vm.state.value.blocked?.detail).isEqualTo(EchoWords.REPLY_NOT_ALLOWED)

        vm.dismissBlocked()
        repo.eligibility = AppResult.Success(TOO_NEW)
        vm.reply(vm.state.value.note!!)
        advanceUntilIdle()
        assertThat(vm.state.value.openComposer).isNull()
        assertThat(vm.state.value.blocked?.title).isEqualTo(EchoWords.ACCOUNT_TOO_NEW)
    }

    @Test
    fun `the author may edit for 30 minutes and delete, anyone else may report`() = runTest(dispatcher) {
        repo.notes["n1"] = AppResult.Success(note("n1", authorId = "me", createdAt = "2026-11-12T09:30:00Z"))
        val vm = model()
        advanceUntilIdle()
        val mine = vm.state.value.note!!
        assertThat(vm.menuFor(mine)).isEqualTo(NoteMenu(edit = true, delete = true, report = false))
        assertThat(vm.menuFor(note("old", authorId = "me", createdAt = "2026-11-12T09:00:00Z")))
            .isEqualTo(NoteMenu(edit = false, delete = true, report = false))
        assertThat(
            vm.menuFor(note("theirs", authorId = "other"))
        ).isEqualTo(NoteMenu(edit = false, delete = false, report = true))

        vm.delete(note("theirs", authorId = "other"))
        advanceUntilIdle()
        assertThat(repo.deletes).isEmpty()
        vm.delete(mine)
        advanceUntilIdle()
        assertThat(repo.deletes).containsExactly("n1")
        assertThat(vm.state.value.deleted).isTrue()
    }

    @Test
    fun `a reply posted from here reloads the replies on return`() = runTest(dispatcher) {
        repo.notes["n1"] = AppResult.Success(note("n1"))
        val vm = model()
        advanceUntilIdle()
        vm.reply(vm.state.value.note!!)
        advanceUntilIdle()
        vm.composerOpened()
        repo.repliesPages[null] = AppResult.Success(NotePage(listOf(note("mine")), null))
        vm.onResume()
        advanceUntilIdle()
        assertThat(vm.replyFeed.value.items.map { it.id }).containsExactly("mine")
    }
}
