package com.us.android.feature.echo

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.echo.data.BookmarkState
import com.us.android.feature.echo.data.EchoErrorCodes
import com.us.android.feature.echo.data.NoteDto
import com.us.android.feature.echo.data.NoteListDto
import com.us.android.feature.echo.data.NotePoll
import com.us.android.feature.echo.data.NoteStatus
import com.us.android.feature.echo.data.PollOption
import com.us.android.feature.echo.data.TrendListDto
import com.us.android.feature.echo.data.ViewBatcher
import com.us.android.feature.echo.data.echoErrorMessage
import com.us.android.feature.echo.data.pollCanVote
import com.us.android.feature.echo.data.pollPercent
import com.us.android.feature.echo.data.pollProblem
import com.us.android.feature.echo.data.pollShowsResults
import com.us.android.feature.echo.data.scheduleProblem
import com.us.android.feature.echo.data.toNote
import com.us.android.feature.echo.data.toTrend
import com.us.android.feature.echo.ui.EchoNoteActions
import com.us.android.feature.echo.ui.mapNote
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.UnconfinedTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.time.Duration
import java.time.Instant

/** Echo v2: the final fixtures, poll and schedule rules, view batches, bookmark and share. */
@OptIn(ExperimentalCoroutinesApi::class)
class EchoV2Test {

    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private fun fixture(name: String): String =
        requireNotNull(
            javaClass.classLoader?.getResource("contracts/notes/$name.json")
        ) { "missing fixture $name" }.readText()

    private val now = Instant.parse("2026-11-12T09:40:00Z")

    // ── fixtures ──

    @Test
    fun `a v2 note reads its views, bookmark, quotes and poll`() {
        val note = json.decodeFromString<ApiEnvelope<NoteDto>>(fixture("note_v2")).data!!.toNote()
        assertThat(note.viewCount).isEqualTo(7)
        assertThat(note.bookmarkCount).isEqualTo(1)
        assertThat(note.bookmarked).isTrue()
        assertThat(note.quoteCount).isEqualTo(1)
        assertThat(note.status).isEqualTo(NoteStatus.PUBLISHED)
        val poll = note.poll!!
        assertThat(poll.options.map { it.label }).containsExactly("Bajji", "Samosa").inOrder()
        assertThat(poll.totalVotes).isEqualTo(2)
        assertThat(poll.viewerVote).isEqualTo(0)
        assertThat(poll.closed).isFalse()
    }

    @Test
    fun `the scheduled list reads status and time, and an empty cursor ends it`() {
        val dto = json.decodeFromString<ApiEnvelope<NoteListDto>>(fixture("note_scheduled")).data!!
        val note = dto.items!!.single().toNote()
        assertThat(note.status).isEqualTo(NoteStatus.SCHEDULED)
        assertThat(note.scheduledAt).isEqualTo("2026-11-12T12:40:00Z")
        assertThat(note.publishError).isNull()
        assertThat(dto.nextCursor).isEmpty()
    }

    @Test
    fun `trends read tag, count and rank, and a closed poll says so`() {
        val trends = json.decodeFromString<ApiEnvelope<TrendListDto>>(
            fixture("trends")
        ).data!!.items!!.map { it.toTrend() }
        assertThat(trends.single().tag).isEqualTo("bengaluru")
        assertThat(trends.single().noteCount).isEqualTo(4)
        assertThat(trends.single().rank).isEqualTo(1)
        val error = json.decodeFromString<ApiEnvelope<NoteDto>>(fixture("error_poll_closed")).error!!
        assertThat(error.code).isEqualTo(EchoErrorCodes.POLL_CLOSED)
        assertThat(
            echoErrorMessage(AppError.Unknown(EchoErrorCodes.POLL_CLOSED, 409))
        ).isEqualTo("This poll has ended.")
    }

    @Test
    fun `the v1 fixtures read as published notes with no poll`() {
        val note = json.decodeFromString<ApiEnvelope<NoteDto>>(fixture("note")).data!!.toNote()
        assertThat(note.status).isEqualTo(NoteStatus.PUBLISHED)
        assertThat(note.poll).isNull()
        assertThat(note.scheduledAt).isNull()
    }

    // ── poll ──

    private fun poll(viewerVote: Int? = null, closed: Boolean = false, votes: Long? = 3) = NotePoll(
        options = listOf(PollOption(0, "A", votes), PollOption(1, "B", votes?.let { 1L })),
        totalVotes = 4,
        endsAt = "2026-11-13T09:40:00Z",
        closed = closed,
        viewerVote = viewerVote,
    )

    @Test
    fun `a poll shows results only after a vote, at the end, or to its author`() {
        assertThat(pollShowsResults(poll(), viewerIsAuthor = false, now = now)).isFalse()
        assertThat(pollShowsResults(poll(viewerVote = 1), viewerIsAuthor = false, now = now)).isTrue()
        assertThat(pollShowsResults(poll(closed = true), viewerIsAuthor = false, now = now)).isTrue()
        assertThat(pollShowsResults(poll(), viewerIsAuthor = true, now = now)).isTrue()
        assertThat(pollShowsResults(poll(), viewerIsAuthor = false, now = now.plus(Duration.ofDays(1)))).isTrue()
        assertThat(pollCanVote(poll(), viewerIsAuthor = false, now = now)).isTrue()
        assertThat(pollCanVote(poll(), viewerIsAuthor = true, now = now)).isFalse()
        assertThat(pollCanVote(poll(viewerVote = 0), viewerIsAuthor = false, now = now)).isFalse()
    }

    @Test
    fun `a withheld count is never drawn as a number`() {
        val hidden = poll(votes = null)
        assertThat(pollPercent(hidden.options[0], hidden.totalVotes)).isNull()
        assertThat(pollPercent(PollOption(0, "A", 3), 4)).isEqualTo(75)
        assertThat(pollPercent(PollOption(0, "A", 0), 0)).isEqualTo(0)
    }

    @Test
    fun `a poll has 2 to 4 distinct choices of up to 25 characters, 5 minutes to 7 days`() {
        assertThat(pollProblem(listOf("a", "b"), 5)).isNull()
        assertThat(pollProblem(listOf("a", "b", "c", "d"), 7 * 24 * 60)).isNull()
        assertThat(pollProblem(listOf("a"), 60)).isNotNull()
        assertThat(pollProblem(listOf("a", "b", "c", "d", "e"), 60)).isNotNull()
        assertThat(pollProblem(listOf("a", " "), 60)).isNotNull()
        assertThat(pollProblem(listOf("a".repeat(25), "b"), 60)).isNull()
        assertThat(pollProblem(listOf("a".repeat(26), "b"), 60)).isNotNull()
        assertThat(pollProblem(listOf("Same", "same"), 60)).isNotNull()
        assertThat(pollProblem(listOf("a", "b"), 4)).isNotNull()
        assertThat(pollProblem(listOf("a", "b"), 7 * 24 * 60 + 1)).isNotNull()
    }

    // ── schedule ──

    @Test
    fun `a post is scheduled 5 minutes to 18 months ahead`() {
        assertThat(scheduleProblem(now.plus(Duration.ofMinutes(5)), now)).isNull()
        assertThat(scheduleProblem(now.plus(Duration.ofMinutes(5)).minusSeconds(1), now)).isNotNull()
        assertThat(scheduleProblem(now.minusSeconds(60), now)).isNotNull()
        assertThat(scheduleProblem(now.plus(Duration.ofDays(540)), now)).isNull()
        assertThat(scheduleProblem(now.plus(Duration.ofDays(540)).plusSeconds(1), now)).isNotNull()
    }

    // ── views ──

    @Test
    fun `a view batch holds each id once, at most 50, and never reports an id twice`() {
        val batcher = ViewBatcher(max = 50)
        batcher.seen(listOf("a", "b", "a", ""))
        batcher.seen(listOf("b", "c"))
        assertThat(batcher.next()).containsExactly("a", "b", "c").inOrder()
        assertThat(batcher.hasPending).isFalse()
        batcher.seen(listOf("a", "d"))
        assertThat(batcher.next()).containsExactly("d")

        val many = ViewBatcher(max = 50)
        many.seen((1..120).map { "n$it" })
        val first = many.next()
        assertThat(first).hasSize(50)
        assertThat(first.toSet()).hasSize(50)
        assertThat(many.next()).hasSize(50)
        assertThat(many.next()).hasSize(20)
        assertThat(many.next()).isEmpty()
    }

    @Test
    fun `a failed batch is retried once, first, then dropped`() {
        val batcher = ViewBatcher(max = 50)
        batcher.seen(listOf("a", "b"))
        val batch = batcher.next()
        batcher.seen(listOf("c"))
        batcher.failed(batch)
        assertThat(batcher.next()).containsExactly("a", "b", "c").inOrder()
        batcher.failed(listOf("a", "b", "c"))
        assertThat(batcher.next()).containsExactly("c")
        batcher.failed(listOf("c"))
        assertThat(batcher.next()).isEmpty()
    }

    // ── bookmark and share ──

    private class Host(scope: TestScope, repo: FakeEchoRepository, start: com.us.android.feature.echo.data.Note) {
        var notes = listOf(start)
        val actions = EchoNoteActions(
            scope = scope,
            deps = testDeps(repo),
            apply = { id, transform -> notes = notes.mapNote(id, transform) },
            remove = { predicate -> notes = notes.filterNot(predicate) },
        )
    }

    @Test
    fun `a bookmark fills at once and takes the server's count`() = runTest(UnconfinedTestDispatcher()) {
        val repo = FakeEchoRepository()
        repo.bookmarkResult = AppResult.Success(BookmarkState(bookmarked = true, count = 9))
        val host = Host(this, repo, note("n1"))
        host.actions.toggleBookmark(host.notes.single())
        advanceUntilIdle()
        assertThat(repo.bookmarkCalls).containsExactly("n1" to true)
        assertThat(host.notes.single().bookmarked).isTrue()
        assertThat(host.notes.single().bookmarkCount).isEqualTo(9)
        assertThat(host.actions.state.value.message).isEqualTo("Added to your Bookmarks")
    }

    @Test
    fun `a refused bookmark puts the note back as it was`() = runTest(UnconfinedTestDispatcher()) {
        val repo = FakeEchoRepository()
        repo.bookmarkResult = AppResult.Failure(AppError.NoNetwork())
        val start = note("n1").copy(bookmarked = true, bookmarkCount = 3)
        val host = Host(this, repo, start)
        host.actions.toggleBookmark(start)
        advanceUntilIdle()
        assertThat(repo.bookmarkCalls).containsExactly("n1" to false)
        assertThat(host.notes.single().bookmarked).isTrue()
        assertThat(host.notes.single().bookmarkCount).isEqualTo(3)
    }

    @Test
    fun `a share or a copied link is recorded on the post`() = runTest(UnconfinedTestDispatcher()) {
        val repo = FakeEchoRepository()
        val host = Host(this, repo, note("n1"))
        host.actions.shared(host.notes.single())
        host.actions.linkCopied(host.notes.single())
        advanceUntilIdle()
        assertThat(repo.shares).containsExactly("n1", "n1")
    }

    @Test
    fun `a vote is sent once, never on one's own poll or after voting`() = runTest(UnconfinedTestDispatcher()) {
        val repo = FakeEchoRepository()
        val open = note("n1").copy(poll = poll())
        val host = Host(this, repo, open)
        host.actions.vote(open, 1)
        host.actions.vote(open.copy(poll = poll(viewerVote = 0)), 1)
        host.actions.vote(note("own", authorId = "me").copy(poll = poll()), 0)
        advanceUntilIdle()
        assertThat(repo.votes).containsExactly("n1" to 1)
    }
}
