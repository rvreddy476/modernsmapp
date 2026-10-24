package com.us.android.feature.feed.ui.hashtag

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.feed.data.hashtag.FollowedHashtag
import com.us.android.core.feed.data.hashtag.HashtagFollowAction
import com.us.android.core.feed.data.hashtag.HashtagFollowRepository
import com.us.android.core.feed.data.hashtag.hashtagFollowRefusal
import com.us.android.core.feed.data.hashtag.hashtagKey
import com.us.android.core.feed.data.hashtag.hashtagLabel
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

// ── The hashtag page's Follow button ───────────────────────────────────

/** The Follow / Following button on a tag's page. [following] null: not read yet, so no button. */
data class HashtagFollowUiState(
    val tag: String = "",
    val following: Boolean? = null,
    val busy: Boolean = false,
    val message: UsMessage? = null,
) {
    /** What the button says: Following once followed, Follow before. */
    val buttonLabel: String get() = if (following == true) "Following" else "Follow"
}

/**
 * Behind the hashtag page's Follow (2026-10-24): `GET` the state when the page
 * opens, `POST` / `DELETE /v1/hashtags/:tag/follow` on a tap. The tap shows
 * at once and the server's answer is what stays; a refusal puts the button
 * back and says why.
 */
@HiltViewModel
class HashtagFollowViewModel @Inject constructor(
    private val repository: HashtagFollowRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(HashtagFollowUiState())
    val state: StateFlow<HashtagFollowUiState> = _state.asStateFlow()

    fun bind(tag: String) {
        val key = hashtagKey(tag)
        if (key.isEmpty() || _state.value.tag == key) return
        // What this session already knows, at once; the server's answer follows.
        _state.value = HashtagFollowUiState(tag = key, following = repository.following.value[key])
        viewModelScope.launch {
            when (val result = repository.state(key)) {
                is AppResult.Success -> _state.update { if (it.busy) it else it.copy(following = result.data.following) }
                // A tag that cannot be read shows no button rather than a wrong one.
                is AppResult.Failure -> Unit
            }
        }
    }

    fun toggle() {
        val now = _state.value
        val was = now.following ?: return
        if (now.busy) return
        _state.update { it.copy(following = !was, busy = true, message = null) }
        viewModelScope.launch {
            val result = repository.setFollowing(now.tag, follow = !was)
            _state.update { current ->
                when (result) {
                    is AppResult.Success -> current.copy(following = result.data.following, busy = false)
                    is AppResult.Failure -> current.copy(
                        following = was,
                        busy = false,
                        message = UsMessage(
                            hashtagFollowRefusal(result.error, if (was) HashtagFollowAction.UNFOLLOW else HashtagFollowAction.FOLLOW),
                        ),
                    )
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}

// ── The followed hashtags list ─────────────────────────────────────────

/** One row: the tag, and whether it is still followed (an unfollowed row stays, with Follow, so it can be undone). */
data class FollowedHashtagRow(val tag: String, val label: String, val following: Boolean = true, val busy: Boolean = false)

data class FollowedHashtagsUiState(
    val loading: Boolean = true,
    val loadError: String? = null,
    val rows: List<FollowedHashtagRow> = emptyList(),
    val nextCursor: String? = null,
    val loadingMore: Boolean = false,
    val message: UsMessage? = null,
) {
    val empty: Boolean get() = !loading && loadError == null && rows.isEmpty()
}

/** Appends a page to the list: a tag already listed is not listed twice. */
internal fun List<FollowedHashtagRow>.plusPage(page: List<FollowedHashtag>): List<FollowedHashtagRow> {
    val listed = mapTo(mutableSetOf()) { it.tag }
    return this + page.filter { listed.add(it.tag) }.map { FollowedHashtagRow(it.tag, it.label) }
}

/**
 * Behind the Followed hashtags page (2026-10-24): `GET /v1/hashtags/followed`
 * a page at a time, newest follow first, and Unfollow / Follow per row.
 */
@HiltViewModel
class FollowedHashtagsViewModel @Inject constructor(
    private val repository: HashtagFollowRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(FollowedHashtagsUiState())
    val state: StateFlow<FollowedHashtagsUiState> = _state.asStateFlow()

    init {
        load()
    }

    fun load() {
        _state.value = FollowedHashtagsUiState(loading = true)
        viewModelScope.launch {
            _state.value = when (val result = repository.followed(PAGE_SIZE, cursor = null)) {
                is AppResult.Success -> FollowedHashtagsUiState(
                    loading = false,
                    rows = emptyList<FollowedHashtagRow>().plusPage(result.data.tags),
                    nextCursor = result.data.nextCursor,
                )
                is AppResult.Failure -> FollowedHashtagsUiState(
                    loading = false,
                    loadError = hashtagFollowRefusal(result.error, HashtagFollowAction.LIST),
                )
            }
        }
    }

    fun loadMore() {
        val now = _state.value
        val cursor = now.nextCursor ?: return
        if (now.loadingMore || now.loading) return
        _state.update { it.copy(loadingMore = true) }
        viewModelScope.launch {
            val result = repository.followed(PAGE_SIZE, cursor)
            _state.update { current ->
                when (result) {
                    is AppResult.Success -> current.copy(
                        loadingMore = false,
                        rows = current.rows.plusPage(result.data.tags),
                        nextCursor = result.data.nextCursor,
                    )
                    is AppResult.Failure -> current.copy(
                        loadingMore = false,
                        message = UsMessage(hashtagFollowRefusal(result.error, HashtagFollowAction.LIST)),
                    )
                }
            }
        }
    }

    /** Unfollow a followed row, or follow an unfollowed one again. */
    fun toggle(tag: String) {
        val row = _state.value.rows.firstOrNull { it.tag == tag } ?: return
        if (row.busy) return
        val follow = !row.following
        updateRow(tag) { it.copy(following = follow, busy = true) }
        viewModelScope.launch {
            when (val result = repository.setFollowing(tag, follow)) {
                is AppResult.Success -> updateRow(tag) { it.copy(following = result.data.following, busy = false) }
                is AppResult.Failure -> {
                    updateRow(tag) { it.copy(following = !follow, busy = false) }
                    val action = if (follow) HashtagFollowAction.FOLLOW else HashtagFollowAction.UNFOLLOW
                    _state.update { it.copy(message = UsMessage(hashtagFollowRefusal(result.error, action))) }
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private fun updateRow(tag: String, change: (FollowedHashtagRow) -> FollowedHashtagRow) =
        _state.update { state -> state.copy(rows = state.rows.map { if (it.tag == tag) change(it) else it }) }

    private companion object {
        /** hashtag_follows_handler.go:95: 50 by default, 100 at most. */
        const val PAGE_SIZE = 50
    }
}

/** "#android" for a row's accessibility label and the page titles. */
internal fun tagTitle(tag: String): String = hashtagLabel(tag)

/** The confirmation a row's Unfollow / Follow leaves, for the screen reader. */
internal fun followChangeLabel(row: FollowedHashtagRow): String =
    if (row.following) "Unfollow ${row.label}" else "Follow ${row.label}"
