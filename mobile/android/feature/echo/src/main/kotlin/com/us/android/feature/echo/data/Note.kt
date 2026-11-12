package com.us.android.feature.echo.data

import androidx.compose.runtime.Immutable

/** Who may reply to a note (echo-contract.md): the author picks one per note. */
enum class ReplyPolicy(val wire: String, val label: String) {
    EVERYONE("everyone", "Everyone"),
    FOLLOWING("following", "People you follow"),
    MENTIONED("mentioned", "Only people you mention"),
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
) {
    val edited: Boolean get() = !editedAt.isNullOrBlank()
}

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

/** The two timelines. Following is the default on first open (design §2). */
enum class Timeline(val label: String) {
    FOLLOWING("Following"),
    FOR_YOU("For You"),
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
)

fun EligibilityDto.toEligibility(): Eligibility =
    Eligibility(canPost = canPost, canReply = canReply, reason = EligibilityReason.fromWire(reason))

private const val KIND_GIF = "gif"
