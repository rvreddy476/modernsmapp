package com.us.android.core.model

/**
 * A long video's cast & crew and its film facts (video credits, founder,
 * 2026-10-23): who is in it and who made it, its genres, its year and its
 * production house. Search reads all of it; the watch page shows it under
 * the description; a tagged account that ACCEPTED shows the video under
 * "Appears in".
 *
 * Empty on every other kind of post and on a row from a server that predates
 * credits, so a FeedItem without them is the post it always was.
 */
data class VideoCredits(
    /** In the creator's order; at most thirty. */
    val credits: List<VideoCredit> = emptyList(),
    /** Genre slugs from `GET /v1/posts/genres`, at most three. */
    val genres: List<String> = emptyList(),
    val releaseYear: Int? = null,
    val productionHouse: String = "",
) {
    /** Nothing at all to show: the watch page draws no Credits block. */
    val isEmpty: Boolean
        get() = credits.isEmpty() && genres.isEmpty() && releaseYear == null && productionHouse.isBlank()

    companion object {
        val None = VideoCredits()
    }
}

/**
 * One credited person.
 *
 * [user] is present ONLY when the tag was accepted — a viewer never sees a
 * pending or declined link, they see the plain [name]. [status] is present
 * ONLY for the post's owner. [linkedUserId] is the owner's view of a link
 * that is not (yet) accepted: kept so an edit can send the same tag back and
 * keep its status; null for every other viewer.
 */
data class VideoCredit(
    val id: String,
    val name: String,
    val role: String,
    val user: CreditedUser? = null,
    val status: CreditStatus? = null,
    val linkedUserId: String? = null,
)

/** The account behind an accepted credit. */
data class CreditedUser(
    val id: String,
    val username: String,
    val displayName: String,
    val avatarUrl: String? = null,
)

/** Where a tag stands — the owner's view only. */
enum class CreditStatus(val wire: String) {
    /** Plain text: never linked, or a link the server would not make. */
    UNLINKED("unlinked"),
    PENDING("pending"),
    ACCEPTED("accepted"),
    DECLINED("declined"),
    ;

    companion object {
        fun fromWire(raw: String?): CreditStatus? {
            val value = raw?.trim()?.lowercase().orEmpty()
            return entries.firstOrNull { it.wire == value }
        }
    }
}
