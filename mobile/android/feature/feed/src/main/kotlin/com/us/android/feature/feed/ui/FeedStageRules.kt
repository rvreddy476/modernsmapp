package com.us.android.feature.feed.ui

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.currentStateAsState
import com.us.android.core.model.FeedDisclosure
import com.us.android.core.model.FeedItem

/*
 * The full-screen feed's rules (post-screens parity, 2026-10-18), pure, so
 * each is a table test without a pager, a player or a server.
 *
 * The pager's pages are the just-published post pinned at page 0 when there
 * is one (the HEAD), then every paged row in order. A page index is
 * therefore not a paging index whenever the head is present — every lookup
 * goes through these, never `items[page]`.
 */

/** How many pages the pager has: one per paged row, one for the head, one for a failed append's retry. */
internal fun stagePageCount(itemCount: Int, hasHead: Boolean, appendFailed: Boolean): Int =
    itemCount + (if (hasHead) 1 else 0) + (if (appendFailed) 1 else 0)

/** The paging index behind [page], or null for the head's page and for anything past the rows. */
internal fun pagingIndexOf(page: Int, hasHead: Boolean, itemCount: Int): Int? {
    val index = page - if (hasHead) 1 else 0
    return index.takeIf { page >= 0 && it in 0 until itemCount }
}

/**
 * The post on [page]: the head on page 0 when there is one, else the row at
 * the page's paging index ([row] — `peek` for a lookup that must not load,
 * `get` for the page being drawn). Null past the end.
 */
internal fun stageItemAt(page: Int, head: FeedItem?, itemCount: Int, row: (Int) -> FeedItem?): FeedItem? {
    if (head != null && page == 0) return head
    return pagingIndexOf(page, head != null, itemCount)?.let(row)
}

/**
 * The pager key for [page]: the post id, so a row inserted or removed above
 * the reader (a hidden post, the head arriving) never moves them off the post
 * they are on. The head's is prefixed so it cannot collide with its own paged
 * row in the moment both exist; the retry page has its own.
 */
internal fun stagePageKey(page: Int, head: FeedItem?, itemCount: Int, peekId: (Int) -> String?): Any {
    if (head != null && page == 0) return HEAD_KEY_PREFIX + head.id
    val index = pagingIndexOf(page, head != null, itemCount) ?: return RETRY_KEY
    return peekId(index) ?: "$UNLOADED_KEY_PREFIX$index"
}

/**
 * The 1-based rank of [page] among the RANKED rows, for the dwell event's
 * position; null on the head, which the server did not rank.
 */
internal fun rankedPosition(page: Int, hasHead: Boolean, itemCount: Int): Int? =
    pagingIndexOf(page, hasHead, itemCount)?.plus(1)

/**
 * The pages to warm around the one the reader settled on: the next
 * [ahead] — photos decoded, videos prepared — and the one behind, so a swipe
 * back is as instant as a swipe on. In order of need; never the settled page
 * itself, never out of range.
 */
internal fun prefetchPages(settled: Int, pageCount: Int, ahead: Int = PREFETCH_AHEAD): List<Int> =
    ((settled + 1)..(settled + ahead)).plus(settled - 1).filter { it in 0 until pageCount && it != settled }

/**
 * Whether coming back to the app should start the feed over: a fresh batch
 * when the reader has been away longer than [keepMillis] (about ten minutes,
 * as the reference feed does), the same place otherwise. Never on a first
 * start ([stoppedAtMillis] null): a cold start is already fresh.
 */
internal fun shouldRefreshOnReturn(stoppedAtMillis: Long?, nowMillis: Long, keepMillis: Long = KEEP_POSITION_MILLIS): Boolean =
    stoppedAtMillis != null && nowMillis - stoppedAtMillis > keepMillis

/** What a page draws for its post. */
internal enum class StageKind { VIDEO, PHOTO, POLL, TEXT }

/**
 * A post's page: VIDEO when it carries a video (playable or still
 * processing — the page then shows its poster), PHOTO when it carries
 * pictures, POLL for a poll, TEXT for words alone.
 */
internal fun stageKind(item: FeedItem): StageKind = when {
    item.media.any { it.kind == VIDEO_MEDIA } -> StageKind.VIDEO
    item.media.isNotEmpty() -> StageKind.PHOTO
    item.poll != null -> StageKind.POLL
    else -> StageKind.TEXT
}

/**
 * The small labels under the author's name for what they declared: a paid
 * partnership, a brand's own post, AI-made content. In that order; none
 * when nothing was declared.
 */
internal fun disclosureLabels(disclosure: FeedDisclosure): List<String> = disclosure.labels

/**
 * Whether the screen is RESUMED — the feed plays only then. Backgrounding,
 * a pushed profile, a tab switch: all pause the feed through this one
 * reading rather than through a listener per surface.
 */
@Composable
internal fun rememberIsResumed(): Boolean {
    val state by LocalLifecycleOwner.current.lifecycle.currentStateAsState()
    return state.isAtLeast(Lifecycle.State.RESUMED)
}

internal const val PAID_PARTNERSHIP = "Paid partnership"
internal const val BRAND = "Brand"
internal const val AI_GENERATED = "AI-generated"

/** The pinned just-published page's key prefix. */
internal const val HEAD_KEY_PREFIX = "head_"
private const val RETRY_KEY = "append_retry"
private const val UNLOADED_KEY_PREFIX = "unloaded_"
private const val VIDEO_MEDIA = "video"

/** Photos decoded and videos buffered this far ahead of the reader. */
internal const val PREFETCH_AHEAD = 3

/** Away longer than this and the feed starts over on return. */
internal const val KEEP_POSITION_MILLIS = 10 * 60 * 1000L

/**
 * People you may know (2026-10-24): the pager page it takes — after the
 * [SUGGESTIONS_AFTER]th post, counting the head — or null when it is not
 * shown ([enabled] false: no one to suggest, or not For You) or fewer posts
 * than that are loaded yet. Pure, so the arithmetic is a table test.
 */
internal fun suggestionsPage(enabled: Boolean, hasHead: Boolean, itemCount: Int): Int? =
    if (!enabled || itemCount < SUGGESTIONS_AFTER) null else SUGGESTIONS_AFTER + if (hasHead) 1 else 0

/** [page] as the post pages number it: one less past the suggestions page, null ON it. */
internal fun postPageOf(page: Int, suggestions: Int?): Int? = when {
    suggestions == null || page < suggestions -> page
    page == suggestions -> null
    else -> page - 1
}

/** How many posts come before People you may know. */
internal const val SUGGESTIONS_AFTER = 4

/** The suggestions page's pager key. */
internal const val SUGGESTIONS_KEY = "people-you-may-know"
