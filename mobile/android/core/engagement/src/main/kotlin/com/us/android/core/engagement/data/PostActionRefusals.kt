package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError

/*
 * Heart only, and no repost on reels (founder, 24 Oct 2026; post-service
 * commit 8370981c4). Every post reaction route takes only `like` and answers
 * any other type with 400 REACTION_NOT_ALLOWED; a reel's repost and quote
 * answer 400 REPOST_NOT_ALLOWED. Both are said as plain sentences, the
 * web's words (postHeart.ts `postActionErrorText`), never as a code.
 *
 * Neither is worth retrying: the server will say the same again, so the
 * failure bar offers Dismiss only for them ([isPostActionRefusal]).
 */

const val REACTION_NOT_ALLOWED = "REACTION_NOT_ALLOWED"
const val REPOST_NOT_ALLOWED = "REPOST_NOT_ALLOWED"

/** The server's contract code on [this] error, where the error carries one. */
fun AppError.postActionCode(): String? = when (this) {
    is AppError.Unknown -> code
    is AppError.Forbidden -> code
    is AppError.Server -> code
    is AppError.NotFound -> code
    is AppError.AuthFailed -> code
    else -> null
}

/** The plain sentence for a refusal from the post-service's reaction or repost routes; null for any other error. */
fun postActionRefusalText(error: AppError): String? = when (error.postActionCode()) {
    REACTION_NOT_ALLOWED -> "Posts take a like only. Tap the heart to like it."
    REPOST_NOT_ALLOWED -> "Reels cannot be reposted. Share the link instead."
    else -> null
}

/** True when [error] is one of the two refusals above, which a retry cannot change. */
fun isPostActionRefusal(error: AppError): Boolean = postActionRefusalText(error) != null
