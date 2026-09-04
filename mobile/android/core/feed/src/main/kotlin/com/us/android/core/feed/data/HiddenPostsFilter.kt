package com.us.android.core.feed.data

import com.us.android.core.engagement.data.HiddenSet
import com.us.android.core.model.FeedItem

/**
 * The feed's view of the process-wide hidden set — "Not interested", Block
 * and Delete from the more sheet, applied as a filter over every page.
 *
 * The set itself lives in `:core:engagement` (see `HiddenPosts` there) so
 * that Settings › Recently deleted can put a restored post back without a
 * feature-to-feature edge; this overload is the only feed-specific part.
 */
fun HiddenSet.hides(item: FeedItem): Boolean = hides(postId = item.id, authorId = item.author.id)
