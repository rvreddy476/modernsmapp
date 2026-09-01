package com.us.android.core.model

/**
 * One notification, as the app reasons about it — Slice D.
 *
 * ## ADDRESS VERSUS IDENTITY
 *
 * [bucket] and [ts] are the ADDRESS: they are the Scylla clustering key and the
 * only way to mark this notification read. [id] is IDENTITY: stable, useful for
 * list diffing, and accepted by nothing on the server.
 *
 * Keeping both, named for what they are, is deliberate. A client that stored
 * only the id would render correctly and be unable to mark anything read.
 */
data class Notification(
    val id: String,
    val bucket: Int,
    val ts: String,
    val kind: NotificationKind,
    val actorUserId: String,
    val entityType: String,
    val entityId: String,
    val target: NotificationTarget,
    val isRead: Boolean,
    val createdAt: String,
) {
    /** The address `POST /v1/notifications/read` requires. */
    val address: NotificationAddress get() = NotificationAddress(bucket, ts)
}

/** The `(bucket, ts)` pair the read endpoint takes. */
data class NotificationAddress(val bucket: Int, val ts: String)

/**
 * What happened, as a closed set the UI can render.
 *
 * ## WHY [Unknown] EXISTS AND KEEPS ITS RAW STRING
 *
 * The server emits notification types this app has no screen for — commerce,
 * dating, food, rider, QA, live — because one notification service serves every
 * vertical in the super-app. A client that crashed, or silently dropped rows,
 * on an unrecognised type would break the inbox every time another team shipped.
 *
 * So unknown types render with a generic line rather than disappearing. The raw
 * value is preserved because a row the user can see but the app cannot explain
 * is still better than a gap in the list, and because the raw string is what
 * makes the gap diagnosable.
 */
sealed interface NotificationKind {
    /** Someone reacted to your post. Server-aggregated over a short window. */
    data object Reaction : NotificationKind

    /** Someone commented on your post. */
    data object Comment : NotificationKind

    /** Someone reacted to your comment. */
    data object CommentReaction : NotificationKind

    /** Someone followed you, or followed a page you own. */
    data object Follow : NotificationKind

    /** Someone mentioned you in a post. */
    data object Mention : NotificationKind

    /** Someone reposted your post. */
    data object Repost : NotificationKind

    /** Someone sent you a connection request. */
    data object ConnectionRequest : NotificationKind

    /** Someone accepted your connection request. */
    data object ConnectionAccepted : NotificationKind

    /** Someone subscribed to you. */
    data object NewSubscriber : NotificationKind

    /** A call rang and nobody answered. Written by the call consumer. */
    data object MissedCall : NotificationKind

    /** A type this build has no rendering for. Carries the wire value. */
    data class Unknown(val raw: String) : NotificationKind

    companion object {
        /** Wire values, from `notification-service/internal/events/consumer.go`. */
        fun fromWire(raw: String): NotificationKind = when (raw) {
            "reaction" -> Reaction
            "comment" -> Comment
            "comment_reaction" -> CommentReaction
            "follow" -> Follow
            "mention" -> Mention
            "post_reposted" -> Repost
            "friend_request" -> ConnectionRequest
            "friend_accepted" -> ConnectionAccepted
            "new_subscriber" -> NewSubscriber
            "missed_call" -> MissedCall
            else -> Unknown(raw)
        }
    }
}

/**
 * Where tapping a notification should go.
 *
 * ## WHY THE SERVER'S deep_link IS PARSED RATHER THAN OBEYED
 *
 * The server sends a path string — `/post/{id}?focusComment={id}`, `/u/{id}`.
 * The app does not navigate by URL: its routes are type-safe Kotlin objects, and
 * handing a raw server string to the navigator would mean any string the backend
 * ever emits becomes a navigation instruction.
 *
 * So the string is parsed into this closed set, and anything unrecognised
 * becomes [None] — a row that renders and does nothing when tapped, rather than
 * a crash or a navigation to somewhere nobody intended.
 */
sealed interface NotificationTarget {
    data class Post(val postId: String) : NotificationTarget
    data class PostComment(val postId: String, val commentId: String) : NotificationTarget
    data class Profile(val userId: String) : NotificationTarget

    /** Unparseable, or a surface this build does not have. Tapping does nothing. */
    data object None : NotificationTarget

    companion object {
        /**
         * Parses the server's deep link.
         *
         * Strict by construction: the id segment must be present and
         * non-blank, and nothing outside the known prefixes resolves. A
         * malformed link degrades to [None] rather than navigating somewhere
         * approximate.
         */
        fun parse(deepLink: String): NotificationTarget {
            val trimmed = deepLink.trim()
            // The contract is an ABSOLUTE PATH. Requiring the leading slash
            // rejects `post/abc`, and — the case that matters —
            // `https://host/post/abc`, which must never be treated as a route.
            if (!trimmed.startsWith(PATH_PREFIX)) return None

            val path = trimmed.substringBefore('?')
            val query = trimmed.substringAfter('?', missingDelimiterValue = "")
            val segments = path.split('/').filter { it.isNotBlank() }

            return when {
                segments.size == 2 && segments[0] == "post" -> {
                    val postId = segments[1]
                    val comment = queryValue(query, "focusComment")
                    if (comment.isNullOrBlank()) {
                        Post(postId)
                    } else {
                        PostComment(postId, comment)
                    }
                }

                segments.size == 2 && segments[0] == "u" -> Profile(segments[1])

                else -> None
            }
        }

        /** The contract is an absolute path; anything else is not a route. */
        private const val PATH_PREFIX = "/"

        private fun queryValue(query: String, key: String): String? = query
            .split('&')
            .firstOrNull { it.substringBefore('=') == key }
            ?.substringAfter('=', missingDelimiterValue = "")
    }
}
