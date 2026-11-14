package com.us.android.feature.echo.data

import androidx.compose.runtime.Immutable

/** Who may reply to a note (echo-contract.md): the author picks one per note. */
enum class ReplyPolicy(val wire: String, val label: String) {
    EVERYONE("everyone", "Everyone"),
    FOLLOWING("following", "Accounts you follow"),
    MENTIONED("mentioned", "Only accounts you mention"),
    ;

    companion object {
        /** An unknown or empty value reads as [EVERYONE], the server's default. */
        fun fromWire(wire: String?): ReplyPolicy = entries.firstOrNull { it.wire == wire } ?: EVERYONE
    }
}

/**
 * The author's mark (design §3.4): Verified person (tick), Official
 * organisation (a different colour), Government (grey). Premium alone shows
 * nothing. Only a verified account carries a mark, whatever its level says.
 */
enum class VerificationMark(val label: String) {
    NONE(""),
    PERSON("Verified"),
    OFFICIAL("Official"),
    GOVERNMENT("Government"),
    ;

    companion object {
        fun of(isVerified: Boolean, level: String): VerificationMark = when {
            !isVerified -> NONE
            level == "organisation" -> OFFICIAL
            level == "government" -> GOVERNMENT
            else -> PERSON
        }
    }
}

@Immutable
data class NoteAuthor(
    val id: String,
    val username: String,
    val displayName: String,
    val avatarUrl: String?,
    val mark: VerificationMark,
) {
    /** The display name, or the handle when there is none. */
    val name: String get() = displayName.ifBlank { username }
    val handle: String get() = if (username.isBlank()) "" else "@$username"
}

/** One image or GIF on a note. The address is read from media-service by id. */
@Immutable
data class NoteMedia(val mediaId: String, val isGif: Boolean, val altText: String)

@Immutable
data class Note(
    val id: String,
    val authorId: String,
    val author: NoteAuthor?,
    val text: String,
    val media: List<NoteMedia>,
    val createdAt: String,
    val editedAt: String?,
    val replyToId: String?,
    val conversationId: String,
    val quoteOfId: String?,
    val replyPolicy: ReplyPolicy,
    val replyCount: Long,
    val likeCount: Long,
    val repostCount: Long,
    val liked: Boolean,
    val reposted: Boolean,
    val viewerCanReply: Boolean,
    /** One level deep; null when there is none, or the quoted note is gone (then [quoteOfId] stays). */
    val quoted: Note?,
    // Echo v2 (echo-v2-contract.md section 1): always present on the wire.
    val viewCount: Long = 0,
    val bookmarkCount: Long = 0,
    val bookmarked: Boolean = false,
    val quoteCount: Long = 0,
    val status: NoteStatus = NoteStatus.PUBLISHED,
    /** RFC3339; set only on the author's scheduled notes. */
    val scheduledAt: String? = null,
    val poll: NotePoll? = null,
    /** Why a due scheduled note did not publish (contract section 3); the author's own list only. */
    val publishError: String? = null,
) {
    val edited: Boolean get() = !editedAt.isNullOrBlank()
}

/** `published`, `scheduled`, or `failed` (a scheduled note dropped after 24 hours of retries). */
enum class NoteStatus(val wire: String) {
    PUBLISHED("published"),
    SCHEDULED("scheduled"),
    FAILED("failed"),
    ;

    companion object {
        fun fromWire(wire: String?): NoteStatus = entries.firstOrNull { it.wire == wire } ?: PUBLISHED
    }
}

/** One choice of a poll. [votes] is null while the server withholds counts from this viewer. */
@Immutable
data class PollOption(val index: Int, val label: String, val votes: Long?)

/** A note's poll. Counts are withheld (null) until the viewer voted, the poll closed, or for the author. */
@Immutable
data class NotePoll(
    val options: List<PollOption>,
    val totalVotes: Long,
    val endsAt: String,
    val closed: Boolean,
    val viewerVote: Int?,
)

/** One trending hashtag (`GET /v1/notes/trends`), without its `#`. */
@Immutable
data class Trend(val tag: String, val noteCount: Long, val rank: Int)

/** Top or Latest: hashtag pages and search. */
enum class NoteSearchTab(val wire: String, val label: String) {
    TOP("top", "Top"),
    LATEST("latest", "Latest"),
}

/** The bookmark answer: the viewer's state and the public count. */
data class BookmarkState(val bookmarked: Boolean, val count: Long)

/** An Echo profile's header, read from the profile service. */
@Immutable
data class EchoProfile(
    val userId: String,
    val username: String,
    val displayName: String,
    val bio: String,
    val avatarMediaId: String?,
    val coverMediaId: String?,
    val mark: VerificationMark,
    val followers: Long,
    val following: Long,
    val joinedAt: String,
    val viewerFollows: Boolean,
) {
    val name: String get() = displayName.ifBlank { username }
    val handle: String get() = if (username.isBlank()) "" else "@$username"
}

/** A new poll as the composer sends it (`poll: {options, duration_minutes}`). */
data class PollDraft(val options: List<String>, val durationMinutes: Int)

/** A page of notes and the server's cursor; null means the last page. */
data class NotePage(val items: List<Note>, val nextCursor: String?)

/** What `GET /v1/notes/me/eligibility` said. */
data class Eligibility(val canPost: Boolean, val canReply: Boolean, val reason: EligibilityReason)

enum class EligibilityReason(val wire: String) {
    NONE(""),
    PREMIUM_REQUIRED("premium_required"),
    ACCOUNT_TOO_NEW("account_too_new"),
    SUSPENDED("suspended"),
    ;

    companion object {
        fun fromWire(wire: String): EligibilityReason = entries.firstOrNull { it.wire == wire } ?: NONE
    }
}

/** A profile's Echo tab: Notes, Replies, Reposts (`?tab=`). */
enum class ProfileNotesTab(val wire: String, val label: String) {
    NOTES("notes", "Notes"),
    REPLIES("replies", "Replies"),
    REPOSTS("reposts", "Reposts"),
}

/**
 * The tabs of Echo's own profile page, X's four. Media has no route of its
 * own: it is the Posts tab narrowed to notes with pictures.
 */
enum class EchoProfileTab(val label: String, val source: ProfileNotesTab, val mediaOnly: Boolean = false) {
    POSTS("Posts", ProfileNotesTab.NOTES),
    REPLIES("Replies", ProfileNotesTab.REPLIES),
    REPOSTS("Reposts", ProfileNotesTab.REPOSTS),
    MEDIA("Media", ProfileNotesTab.NOTES, mediaOnly = true),
}

/**
 * The two timelines, in X's order: For you, then Following. Following is
 * still the one selected on first open (design section 2, decision kept).
 */
enum class Timeline(val label: String) {
    FOR_YOU("For you"),
    FOLLOWING("Following"),
}

fun NoteDto.toNote(): Note = Note(
    id = id,
    authorId = authorId,
    author = author?.let {
        NoteAuthor(
            id = it.id.ifBlank { authorId },
            username = it.username,
            displayName = it.displayName,
            avatarUrl = it.avatarUrl?.takeIf { url -> url.isNotBlank() },
            mark = VerificationMark.of(it.isVerified, it.verificationLevel),
        )
    },
    text = text,
    media = media.orEmpty()
        .sortedBy { it.position }
        .filter { it.mediaId.isNotBlank() }
        .map { NoteMedia(it.mediaId, isGif = it.kind == KIND_GIF, altText = it.altText) },
    createdAt = createdAt,
    editedAt = editedAt?.takeIf { it.isNotBlank() },
    replyToId = replyToId?.takeIf { it.isNotBlank() },
    conversationId = conversationId?.takeIf { it.isNotBlank() } ?: id,
    quoteOfId = quoteOfId?.takeIf { it.isNotBlank() },
    replyPolicy = ReplyPolicy.fromWire(replyPolicy),
    replyCount = replyCount,
    likeCount = likeCount,
    repostCount = repostCount,
    liked = viewerHasLiked,
    reposted = viewerHasReposted,
    viewerCanReply = viewerCanReply,
    quoted = quotedNote?.toNote(),
    viewCount = viewCount,
    bookmarkCount = bookmarkCount,
    bookmarked = viewerHasBookmarked,
    quoteCount = quoteCount,
    status = NoteStatus.fromWire(status),
    scheduledAt = scheduledAt?.takeIf { it.isNotBlank() },
    poll = poll?.toPoll(),
    publishError = publishError?.takeIf { it.isNotBlank() },
)

fun PollDto.toPoll(): NotePoll = NotePoll(
    options = options.orEmpty().sortedBy { it.index }.map { PollOption(it.index, it.label, it.votes) },
    totalVotes = totalVotes,
    endsAt = endsAt,
    closed = closed,
    viewerVote = viewerVote,
)

fun TrendDto.toTrend(): Trend = Trend(tag = tag.removePrefix("#"), noteCount = noteCount, rank = rank)

fun EligibilityDto.toEligibility(): Eligibility =
    Eligibility(canPost = canPost, canReply = canReply, reason = EligibilityReason.fromWire(reason))

private const val KIND_GIF = "gif"
