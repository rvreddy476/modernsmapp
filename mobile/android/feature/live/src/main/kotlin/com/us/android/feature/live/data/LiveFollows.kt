package com.us.android.feature.live.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.feed.data.FollowGraph
import com.us.android.core.model.FollowStatus
import kotlinx.coroutines.flow.StateFlow
import javax.inject.Inject

/**
 * Who the viewer is and whom they follow, as the live screens need it — a
 * seam over the app's shared [FollowGraph] (graph-service, through the same
 * ProfileRepository the profile screen uses), so a follow made in a live
 * room is already applied on Home, Reels and Search, and the ViewModels are
 * tested without the graph's HTTP stack.
 */
interface LiveFollows {
    /** The signed-in user, or blank before the session resolves (or when signed out). */
    val ownId: String

    /** User id → the viewer's edge toward them. Absent means not yet known. */
    val edges: StateFlow<Map<String, FollowStatus>>

    suspend fun ensureKnown(userIds: Collection<String>)

    suspend fun follow(userId: String): AppResult<Unit>
}

/** The real one: [FollowGraph] itself. */
class FollowGraphLiveFollows @Inject constructor(private val graph: FollowGraph) : LiveFollows {
    override val ownId: String get() = graph.ownId
    override val edges: StateFlow<Map<String, FollowStatus>> get() = graph.edges

    override suspend fun ensureKnown(userIds: Collection<String>) = graph.ensureKnown(userIds)

    override suspend fun follow(userId: String): AppResult<Unit> = graph.follow(userId)
}
