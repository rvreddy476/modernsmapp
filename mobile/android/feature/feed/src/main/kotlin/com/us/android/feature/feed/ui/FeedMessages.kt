package com.us.android.feature.feed.ui

import com.us.android.core.common.error.AppError
import com.us.android.feature.feed.data.AppErrorException

/**
 * One wording per failure class, shared by every feed surface so the home
 * timeline, Friends, a tag's posts and the trending list cannot drift into
 * four phrasings of "offline".
 */
internal fun AppError?.feedMessage(): String = when (this) {
    is AppError.NoNetwork -> "You're offline. Check your connection and try again."
    is AppError.Timeout -> "That took too long. Try again."
    is AppError.AuthFailed -> "Please sign in again to see your feed."
    else -> "We couldn't load the feed."
}

/**
 * Paging carries errors as `Throwable`, so the typed [AppError] the network
 * layer produced is re-read here rather than reduced to a generic message.
 */
internal fun Throwable.feedMessage(): String = (this as? AppErrorException)?.error.feedMessage()
