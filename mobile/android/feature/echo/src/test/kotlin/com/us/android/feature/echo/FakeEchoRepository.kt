package com.us.android.feature.echo

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.PostShareChats
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.model.SessionState
import com.us.android.feature.echo.data.BookmarkState
import com.us.android.feature.echo.data.EchoPeople
import com.us.android.feature.echo.data.EchoRepository
import com.us.android.feature.echo.data.EchoViews
import com.us.android.feature.echo.data.Eligibility
import com.us.android.feature.echo.data.EligibilityReason
import com.us.android.feature.echo.data.Note
import com.us.android.feature.echo.data.NotePage
import com.us.android.feature.echo.data.NoteSearchTab
import com.us.android.feature.echo.data.PollDraft
import com.us.android.feature.echo.data.ProfileNotesTab
import com.us.android.feature.echo.data.ReplyPolicy
import com.us.android.feature.echo.data.Timeline
import com.us.android.feature.echo.data.Trend
import com.us.android.feature.echo.ui.EchoActionDeps
import io.mockk.every
import io.mockk.mockk
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import java.time.Clock
import java.time.Instant
import java.time.ZoneOffset

/** Builds a note for tests. */
fun note(
    id: String,
    authorId: String = "author",
    createdAt: String = "2026-11-12T09:30:00Z",
    canReply: Boolean = true,
    liked: Boolean = false,
    likes: Long = 0,
    reposted: Boolean = false,
    reposts: Long = 0,
    text: String = "text $id",
    policy: ReplyPolicy = ReplyPolicy.EVERYONE,
    replyToId: String? = null,
) = Note(
    id = id, authorId = authorId, author = null, text = text, media = emptyList(), createdAt = createdAt,
    editedAt = null, replyToId = replyToId, conversationId = id, quoteOfId = null, replyPolicy = policy,
    replyCount = 0, likeCount = likes,
    repostCount = reposts, liked = liked, reposted = reposted, viewerCanReply = canReply, quoted = null,
)

val PREMIUM = Eligibility(canPost = true, canReply = true, reason = EligibilityReason.NONE)
val NOT_PREMIUM = Eligibility(canPost = false, canReply = true, reason = EligibilityReason.PREMIUM_REQUIRED)
val TOO_NEW = Eligibility(canPost = false, canReply = false, reason = EligibilityReason.ACCOUNT_TOO_NEW)
val SUSPENDED = Eligibility(canPost = false, canReply = false, reason = EligibilityReason.SUSPENDED)

/** A scripted [EchoRepository] that records what was sent. */
class FakeEchoRepository : EchoRepository {
    var eligibility: AppResult<Eligibility> = AppResult.Success(PREMIUM)
    val timelines = mutableMapOf<Pair<Timeline, String?>, AppResult<NotePage>>()
    val notes = mutableMapOf<String, AppResult<Note>>()
    val repliesPages = mutableMapOf<String?, AppResult<NotePage>>()
    val userPages = mutableMapOf<Pair<ProfileNotesTab, String?>, AppResult<NotePage>>()
    var conversationResult: AppResult<List<Note>> = AppResult.Success(emptyList())
    var postResult: AppResult<Note> = AppResult.Success(com.us.android.feature.echo.note("posted"))
    var threadResult: AppResult<List<Note>> = AppResult.Success(
        listOf(com.us.android.feature.echo.note("t1"), com.us.android.feature.echo.note("t2"))
    )
    var editResult: AppResult<Note> = AppResult.Success(com.us.android.feature.echo.note("edited"))
    var likeResult: AppResult<Unit> = AppResult.Success(Unit)
    var repostResult: AppResult<Unit> = AppResult.Success(Unit)
    var deleteResult: AppResult<Unit> = AppResult.Success(Unit)

    var eligibilityReads = 0
    val timelineCalls = mutableListOf<Pair<Timeline, String?>>()
    val posts = mutableListOf<Posted>()
    val threads = mutableListOf<Pair<String, List<Pair<String, List<String>>>>>()
    val edits = mutableListOf<Triple<String, String?, ReplyPolicy?>>()
    val likes = mutableListOf<Pair<String, Boolean>>()
    val reposts = mutableListOf<Pair<String, Boolean>>()
    val deletes = mutableListOf<String>()

    data class Posted(
        val key: String,
        val text: String,
        val mediaIds: List<String>,
        val replyToId: String?,
        val quoteOfId: String?,
        val policy: ReplyPolicy?,
        val poll: PollDraft? = null,
        val scheduledAt: String? = null,
    )

    override suspend fun timeline(timeline: Timeline, cursor: String?): AppResult<NotePage> {
        timelineCalls += timeline to cursor
        return timelines[timeline to cursor] ?: AppResult.Success(NotePage(emptyList(), null))
    }

    override suspend fun note(id: String): AppResult<Note> =
        notes[id] ?: AppResult.Failure(AppError.NotFound(code = "NOTE_NOT_FOUND"))

    override suspend fun conversation(id: String): AppResult<List<Note>> = conversationResult

    override suspend fun replies(id: String, cursor: String?): AppResult<NotePage> =
        repliesPages[cursor] ?: AppResult.Success(NotePage(emptyList(), null))

    override suspend fun byUser(userId: String, tab: ProfileNotesTab, cursor: String?): AppResult<NotePage> =
        userPages[tab to cursor] ?: AppResult.Success(NotePage(emptyList(), null))

    override suspend fun eligibility(): AppResult<Eligibility> {
        eligibilityReads++
        return eligibility
    }

    override suspend fun post(
        idempotencyKey: String,
        text: String,
        mediaIds: List<String>,
        replyToId: String?,
        quoteOfId: String?,
        replyPolicy: ReplyPolicy?,
        poll: PollDraft?,
        scheduledAt: String?,
    ): AppResult<Note> {
        posts += Posted(idempotencyKey, text, mediaIds, replyToId, quoteOfId, replyPolicy, poll, scheduledAt)
        return postResult
    }

    override suspend fun postThread(
        idempotencyKey: String,
        entries: List<Pair<String, List<String>>>,
        replyPolicy: ReplyPolicy?,
    ): AppResult<List<Note>> {
        threads += idempotencyKey to entries
        return threadResult
    }

    override suspend fun edit(id: String, text: String?, replyPolicy: ReplyPolicy?): AppResult<Note> {
        edits += Triple(id, text, replyPolicy)
        return editResult
    }

    override suspend fun delete(id: String): AppResult<Unit> {
        deletes += id
        return deleteResult
    }

    override suspend fun setLiked(id: String, liked: Boolean): AppResult<Unit> {
        likes += id to liked
        return likeResult
    }

    override suspend fun setReposted(id: String, reposted: Boolean): AppResult<Unit> {
        reposts += id to reposted
        return repostResult
    }

    override suspend fun report(id: String, reason: String, details: String): ReportOutcome = ReportOutcome.Filed

    // ── Echo v2 ──
    var voteResult: AppResult<Note>? = null
    var bookmarkResult: AppResult<BookmarkState>? = null
    var shareResult: AppResult<Unit> = AppResult.Success(Unit)
    var viewsResult: AppResult<Unit> = AppResult.Success(Unit)
    var rescheduleResult: AppResult<Note> = AppResult.Success(com.us.android.feature.echo.note("rescheduled"))
    var trendsResult: AppResult<List<Trend>> = AppResult.Success(emptyList())
    val votes = mutableListOf<Pair<String, Int>>()
    val bookmarkCalls = mutableListOf<Pair<String, Boolean>>()
    val shares = mutableListOf<String>()
    val viewBatches = mutableListOf<List<String>>()
    val reschedules = mutableListOf<Pair<String, String>>()
    val scheduledDeletes = mutableListOf<String>()

    override suspend fun vote(id: String, optionIndex: Int): AppResult<Note> {
        votes += id to optionIndex
        return voteResult ?: AppResult.Success(com.us.android.feature.echo.note(id))
    }

    override suspend fun scheduled(cursor: String?): AppResult<NotePage> = AppResult.Success(
        NotePage(emptyList(), null)
    )

    override suspend fun reschedule(id: String, scheduledAt: String): AppResult<Note> {
        reschedules += id to scheduledAt
        return rescheduleResult
    }

    override suspend fun deleteScheduled(id: String): AppResult<Unit> {
        scheduledDeletes += id
        return AppResult.Success(Unit)
    }

    override suspend fun setBookmarked(id: String, bookmarked: Boolean): AppResult<BookmarkState> {
        bookmarkCalls += id to bookmarked
        return bookmarkResult ?: AppResult.Success(BookmarkState(bookmarked, if (bookmarked) 1 else 0))
    }

    override suspend fun bookmarks(cursor: String?): AppResult<NotePage> = AppResult.Success(
        NotePage(emptyList(), null)
    )

    override suspend fun recordViews(ids: List<String>): AppResult<Unit> {
        viewBatches += ids
        return viewsResult
    }

    override suspend fun quotes(id: String, cursor: String?): AppResult<NotePage> = AppResult.Success(
        NotePage(emptyList(), null)
    )

    override suspend fun trends(): AppResult<List<Trend>> = trendsResult

    override suspend fun hashtag(tag: String, tab: NoteSearchTab, cursor: String?): AppResult<NotePage> =
        AppResult.Success(NotePage(emptyList(), null))

    override suspend fun search(query: String, tab: NoteSearchTab, cursor: String?): AppResult<NotePage> =
        AppResult.Success(NotePage(emptyList(), null))

    override suspend fun recordShare(id: String): AppResult<Unit> {
        shares += id
        return shareResult
    }
}

/** Note actions over [repo]: the viewer is [viewerId]; chats and views are inert. */
fun testDeps(repo: EchoRepository, viewerId: String = "me"): EchoActionDeps = EchoActionDeps(
    repository = repo,
    people = mockk<EchoPeople>(relaxed = true).also { every { it.viewerId } returns viewerId },
    chats = mockk<PostShareChats>(relaxed = true),
    views = mockk<EchoViews>(relaxed = true),
    clock = Clock.fixed(Instant.parse("2026-11-12T09:40:00Z"), ZoneOffset.UTC),
)

class FakeSession(userId: String = "me") : SessionStateProvider {
    override val sessionState: StateFlow<SessionState> = MutableStateFlow(SessionState.Authenticated(userId, "s1"))
}
