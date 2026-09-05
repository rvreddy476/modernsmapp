package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import javax.inject.Inject
import javax.inject.Singleton

/** Someone the viewer may know. */
data class PersonSuggestion(
    val userId: String,
    /** Blank when the engine had no name; the surface resolves the profile. */
    val displayName: String,
    val score: Double,
    val explainText: String,
    val mutualFriendCount: Int,
    val reasonCodes: List<String>,
)

/** A community the viewer may want to join. */
data class CommunitySuggestion(
    val communityId: String,
    val handle: String,
    val name: String,
    val description: String,
    val avatarMediaId: String?,
    val memberCount: Int,
    val explainText: String,
    val reasonCodes: List<String>,
) {
    val handleForDisplay: String get() = "@$handle"
}

/** The actions the engine accepts back. */
enum class SuggestionAction(val wire: String) {
    Dismiss("dismiss"),
    Follow("follow"),
    FriendRequest("friend_request"),
    Hide("hide"),
    Block("block"),
}

/**
 * Suggestions over [SuggestionsApi]. Impressions and actions are
 * best-effort telemetry: their failures are returned, never surfaced.
 */
@Singleton
class SuggestionsRepository @Inject constructor(
    private val api: SuggestionsApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun people(limit: Int = PEOPLE_LIMIT): AppResult<List<PersonSuggestion>> =
        apiCall(errorMapper, { page -> page.items.map { it.toDomain() } }) { api.people(limit) }

    suspend fun communities(limit: Int = COMMUNITY_LIMIT): AppResult<List<CommunitySuggestion>> =
        apiCall(errorMapper, { page -> page.items.map { it.toDomain() } }) { api.communities(limit) }

    suspend fun personAction(userId: String, action: SuggestionAction): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.action(
                SuggestionActionRequest(
                    type = TYPE_FRIEND,
                    surface = SURFACE_CHAT,
                    action = action.wire,
                    candidateUserId = userId,
                ),
            )
        }

    suspend fun communityAction(communityId: String, action: SuggestionAction): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.action(
                SuggestionActionRequest(
                    type = TYPE_COMMUNITY,
                    surface = SURFACE_CHAT,
                    action = action.wire,
                    communityId = communityId,
                ),
            )
        }

    suspend fun peopleImpression(people: List<PersonSuggestion>): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.impression(
                SuggestionImpressionRequest(
                    type = TYPE_FRIEND,
                    surface = SURFACE_CHAT,
                    items = people.mapIndexed { index, person ->
                        SuggestionImpressionItem(
                            candidateUserId = person.userId,
                            rank = index + 1,
                            score = person.score
                        )
                    },
                ),
            )
        }

    suspend fun communityImpression(communities: List<CommunitySuggestion>): AppResult<Unit> =
        noContentApiCall(errorMapper) {
            api.impression(
                SuggestionImpressionRequest(
                    type = TYPE_COMMUNITY,
                    surface = SURFACE_CHAT,
                    items = communities.mapIndexed { index, community ->
                        SuggestionImpressionItem(communityId = community.communityId, rank = index + 1)
                    },
                ),
            )
        }

    companion object {
        const val PEOPLE_LIMIT = 20
        const val COMMUNITY_LIMIT = 10
        const val TYPE_FRIEND = "friend"
        const val TYPE_COMMUNITY = "community"
        const val SURFACE_CHAT = "chat"
    }
}

private fun PersonSuggestionDto.toDomain() = PersonSuggestion(
    userId = candidateUserId,
    displayName = displayName,
    score = score,
    explainText = explainText,
    mutualFriendCount = mutualFriendCount,
    reasonCodes = reasonCodes,
)

private fun CommunitySuggestionDto.toDomain() = CommunitySuggestion(
    communityId = communityId,
    handle = handle,
    name = name,
    description = description,
    avatarMediaId = avatarMediaId,
    memberCount = memberCount,
    explainText = explainText,
    reasonCodes = reasonCodes,
)
