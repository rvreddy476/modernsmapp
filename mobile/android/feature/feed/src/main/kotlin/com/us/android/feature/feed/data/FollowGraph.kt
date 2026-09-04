package com.us.android.feature.feed.data

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.FollowStatus
import com.us.android.core.model.SessionState
import com.us.android.core.profile.data.ProfileRepository
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The viewer's follow edges toward the authors on screen — what decides
 * whether a card's header or a reel's overlay offers "Follow".
 *
 * ## WHY A PROCESS-WIDE CACHE
 *
 * A feed row carries no relationship field; the only source is
 * `GET /v1/graph/relationship`, one call per author. Fired per ROW from
 * inside a scrolling list that would be the classic N+1. Keyed per unique
 * author and kept for the life of the process, it is one call per person
 * the viewer ever scrolls past — the same author on Home, Friends and Reels
 * is fetched once — and a follow made on a reel is already applied when the
 * same author's post scrolls past on Home.
 *
 * Writes go through the same [ProfileRepository] the profile screen uses,
 * optimistically: the edge flips at once and is put back if the server says
 * no. A private account answers "requested" rather than "followed"; that is
 * recorded as [FollowStatus.REQUESTED], which hides the button just as a
 * real follow does — asking twice is the one thing the control must not do.
 */
@Singleton
class FollowGraph @Inject constructor(
    private val profiles: ProfileRepository,
    private val session: SessionStateProvider,
) {
    private val _edges = MutableStateFlow<Map<String, FollowStatus>>(emptyMap())

    /** Author id → the viewer's edge toward them. Absent means not yet known. */
    val edges: StateFlow<Map<String, FollowStatus>> = _edges.asStateFlow()

    private val inFlight = mutableSetOf<String>()
    private val lock = Mutex()

    /** The signed-in user, or blank before the session resolves. */
    val ownId: String
        get() = (session.sessionState.value as? SessionState.Authenticated)?.userId.orEmpty()

    /**
     * Fetches the edge for every author not yet known. Each id is asked for
     * once even when two pages hydrate at the same moment; a failed lookup is
     * simply not recorded, so the button stays hidden rather than guessing.
     */
    suspend fun ensureKnown(authorIds: Collection<String>) {
        val viewer = ownId
        if (viewer.isBlank()) return
        val wanted = lock.withLock {
            authorIds.asSequence()
                .filter { it.isNotBlank() && it != viewer }
                .filter { it !in _edges.value && it !in inFlight }
                .distinct()
                .toList()
                .also { inFlight += it }
        }
        if (wanted.isEmpty()) return
        try {
            coroutineScope {
                wanted.map { id -> async { id to profiles.relationship(viewer, id) } }.awaitAll()
            }.forEach { (id, result) ->
                if (result is AppResult.Success) {
                    _edges.update { it + (id to result.data.followStatus) }
                }
            }
        } finally {
            lock.withLock { inFlight -= wanted.toSet() }
        }
    }

    /** Follows [authorId], optimistically. Returns the server's verdict. */
    suspend fun follow(authorId: String): AppResult<Unit> {
        val before = _edges.value[authorId] ?: FollowStatus.NONE
        _edges.update { it + (authorId to FollowStatus.FOLLOWING) }
        return when (val result = profiles.follow(authorId)) {
            is AppResult.Success -> {
                val status = if (result.data == REQUESTED) FollowStatus.REQUESTED else FollowStatus.FOLLOWING
                _edges.update { it + (authorId to status) }
                AppResult.Success(Unit)
            }
            is AppResult.Failure -> {
                _edges.update { it + (authorId to before) }
                result
            }
        }
    }

    /** Unfollows [authorId], optimistically. */
    suspend fun unfollow(authorId: String): AppResult<Unit> {
        val before = _edges.value[authorId] ?: FollowStatus.FOLLOWING
        _edges.update { it + (authorId to FollowStatus.NONE) }
        return when (val result = profiles.unfollow(authorId)) {
            is AppResult.Success -> AppResult.Success(Unit)
            is AppResult.Failure -> {
                _edges.update { it + (authorId to before) }
                result
            }
        }
    }

    private companion object {
        const val REQUESTED = "requested"
    }
}

/**
 * Whether a surface should offer "Follow" for [authorId].
 *
 * Only when the answer is KNOWN to be "not following": never for the viewer's
 * own posts, never for an author already followed or requested, and never
 * while the edge is still unknown — a Follow button that appears and then
 * vanishes when the real answer lands is worse than one that arrives late.
 */
fun offersFollow(ownId: String, authorId: String, edge: FollowStatus?): Boolean =
    authorId.isNotBlank() && authorId != ownId && edge == FollowStatus.NONE
