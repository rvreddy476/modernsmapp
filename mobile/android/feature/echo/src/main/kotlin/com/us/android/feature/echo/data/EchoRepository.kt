package com.us.android.feature.echo.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.engagement.data.EngagementWrites
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** Every Echo read and write the screens make. An interface so the ViewModels test against fakes. */
@Suppress("TooManyFunctions") // the contract's routes plus the shared heart, repost and report
interface EchoRepository {
    suspend fun timeline(timeline: Timeline, cursor: String?): AppResult<NotePage>
    suspend fun note(id: String): AppResult<Note>
    suspend fun conversation(id: String): AppResult<List<Note>>
    suspend fun replies(id: String, cursor: String?): AppResult<NotePage>
    suspend fun byUser(userId: String, tab: ProfileNotesTab, cursor: String?): AppResult<NotePage>
    suspend fun eligibility(): AppResult<Eligibility>

    /** A single note: top-level, a reply ([replyToId]) or a quote ([quoteOfId]). */
    suspend fun post(
        idempotencyKey: String,
        text: String,
        mediaIds: List<String>,
        replyToId: String? = null,
        quoteOfId: String? = null,
        replyPolicy: ReplyPolicy? = null,
    ): AppResult<Note>

    /** A thread of 2–25 entries, each `text` with its media ids. */
    suspend fun postThread(
        idempotencyKey: String,
        entries: List<Pair<String, List<String>>>,
        replyPolicy: ReplyPolicy?,
    ): AppResult<List<Note>>

    suspend fun edit(id: String, text: String?, replyPolicy: ReplyPolicy?): AppResult<Note>
    suspend fun delete(id: String): AppResult<Unit>

    /** Heart on or off (the existing `like` reaction: idempotent both ways). */
    suspend fun setLiked(id: String, liked: Boolean): AppResult<Unit>

    /** Plain repost on or off (`type=plain`). A quote is [post] with `quoteOfId`. */
    suspend fun setReposted(id: String, reposted: Boolean): AppResult<Unit>

    suspend fun report(id: String, reason: String, details: String): ReportOutcome
}

@Singleton
class DefaultEchoRepository @Inject constructor(
    private val api: EchoApi,
    private val errorMapper: ErrorMapper,
    private val engagement: EngagementWrites,
    private val reports: ReportRepository,
) : EchoRepository {

    override suspend fun timeline(timeline: Timeline, cursor: String?): AppResult<NotePage> = page {
        when (timeline) {
            Timeline.FOLLOWING -> api.following(cursor, PAGE_SIZE)
            Timeline.FOR_YOU -> api.forYou(cursor, PAGE_SIZE)
        }
    }

    override suspend fun note(id: String): AppResult<Note> =
        apiCall(errorMapper) { api.note(id) }.map { it.toNote() }

    override suspend fun conversation(id: String): AppResult<List<Note>> =
        page { api.conversation(id) }.map { it.items }

    override suspend fun replies(id: String, cursor: String?): AppResult<NotePage> =
        page { api.replies(id, cursor, PAGE_SIZE) }

    override suspend fun byUser(userId: String, tab: ProfileNotesTab, cursor: String?): AppResult<NotePage> =
        page { api.byUser(userId, tab.wire, cursor, PAGE_SIZE) }

    override suspend fun eligibility(): AppResult<Eligibility> =
        apiCall(errorMapper) { api.eligibility() }.map { it.toEligibility() }

    override suspend fun post(
        idempotencyKey: String,
        text: String,
        mediaIds: List<String>,
        replyToId: String?,
        quoteOfId: String?,
        replyPolicy: ReplyPolicy?,
    ): AppResult<Note> = apiCall(errorMapper) {
        api.create(
            idempotencyKey,
            CreateNoteRequest(
                text = text.trim(),
                mediaIds = mediaIds.takeIf { it.isNotEmpty() },
                replyToId = replyToId,
                quoteOfId = quoteOfId,
                replyPolicy = replyPolicy?.wire,
            ),
        )
    }.map { it.toNote() }

    override suspend fun postThread(
        idempotencyKey: String,
        entries: List<Pair<String, List<String>>>,
        replyPolicy: ReplyPolicy?,
    ): AppResult<List<Note>> = apiCall(errorMapper) {
        api.createThread(
            idempotencyKey,
            CreateThreadRequest(
                entries = entries.map { (text, media) ->
                    ThreadEntryRequest(
                        text.trim(),
                        media.takeIf { it.isNotEmpty() }
                    )
                },
                replyPolicy = replyPolicy?.wire,
            ),
        )
    }.map { dto -> dto.notes.orEmpty().map { it.toNote() } }

    override suspend fun edit(id: String, text: String?, replyPolicy: ReplyPolicy?): AppResult<Note> =
        apiCall(errorMapper) { api.edit(id, EditNoteRequest(text = text?.trim(), replyPolicy = replyPolicy?.wire)) }
            .map { it.toNote() }

    override suspend fun delete(id: String): AppResult<Unit> = noContentApiCall(errorMapper) { api.delete(id) }

    override suspend fun setLiked(id: String, liked: Boolean): AppResult<Unit> =
        if (liked) engagement.react(id) else engagement.unreact(id)

    override suspend fun setReposted(id: String, reposted: Boolean): AppResult<Unit> =
        if (reposted) engagement.repost(id) else engagement.removeRepost(id)

    override suspend fun report(id: String, reason: String, details: String): ReportOutcome =
        reports.reportPost(id, reason, details)

    private suspend inline fun page(crossinline block: suspend () -> ApiEnvelope<NoteListDto>): AppResult<NotePage> =
        apiCall(errorMapper) { block() }.map { dto ->
            NotePage(
                items = dto.items.orEmpty().map { it.toNote() },
                nextCursor = dto.nextCursor?.takeIf { it.isNotBlank() },
            )
        }

    private companion object {
        const val PAGE_SIZE = 20
    }
}
