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

/** One row of results. Several kinds, one list: the page draws each by its shape. */
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

    /** A tag, kept without its `#`; [id] is the tag itself. */
    data class Hashtag(val tag: String) : SearchHit {
        override val id: String get() = tag
        val label: String get() = "#$tag"
    }

    /** A sound from media-service's sound search; [usageCount] is how many reels play it. */
    data class Sound(
        override val id: String,
        val title: String,
        val artist: String,
        val durationMs: Long,
        val usageCount: Long,
        val isOriginal: Boolean,
    ) : SearchHit {
        val titleForDisplay: String get() = title.ifBlank { if (isOriginal) "Original sound" else "Untitled sound" }
    }
}

/** One of the signed-in viewer's past searches, as search-service keeps it; [id] is what `DELETE` takes. */
data class RecentSearch(val id: String, val query: String)

/**
 * A suggestion while typing, from `GET v1/search/autocomplete`. A person
 * opens their profile; a tag opens its page; anything else is a query to
 * search for.
 */
sealed interface SearchSuggestion {
    data class Person(val userId: String, val username: String, val displayName: String) : SearchSuggestion {
        val nameForDisplay: String get() = displayName.ifBlank { username.ifBlank { "Unnamed" } }
        val handle: String? get() = username.takeIf { it.isNotBlank() }?.let { "@$it" }
    }

    data class Tag(val tag: String) : SearchSuggestion

    data class Query(val text: String) : SearchSuggestion
}
