package com.us.android.feature.search.data

/** Who a post or video row credits. */
data class SearchAuthor(
    val id: String,
    val displayName: String,
    val username: String,
    val avatarUrl: String?,
) {
    val nameForDisplay: String get() = displayName.ifBlank { username.ifBlank { "Unnamed" } }
    val handle: String? get() = username.takeIf { it.isNotBlank() }?.let { "@$it" }
}

/** One row of results. Four kinds, one list: the page draws each by its shape. */
sealed interface SearchHit {
    val id: String

    data class User(
        override val id: String,
        val username: String,
        val displayName: String,
        val avatarUrl: String?,
    ) : SearchHit {
        val nameForDisplay: String get() = displayName.ifBlank { username.ifBlank { "Unnamed" } }
        val handle: String? get() = username.takeIf { it.isNotBlank() }?.let { "@$it" }
    }

    data class Post(
        override val id: String,
        val author: SearchAuthor,
        val text: String,
        val title: String,
        val createdAt: String,
    ) : SearchHit

    /**
     * A reel or a long video — [isReel] says which, so the tap knows whether
     * to open Reels or the watch screen. [durationMs] is 0 when unknown, and
     * the pill is then not drawn.
     */
    data class Video(
        override val id: String,
        val title: String,
        val author: SearchAuthor,
        val thumbnailUrl: String?,
        val durationMs: Long,
        val createdAt: String,
        val isReel: Boolean,
    ) : SearchHit

    data class Channel(
        override val id: String,
        val name: String,
        val handle: String,
        val avatarUrl: String?,
        val videoCount: Int,
    ) : SearchHit {
        val handleForDisplay: String get() = "@$handle"
    }
}
