package com.us.android.core.profile.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.profile.data.dto.AboutItemDto
import kotlinx.coroutines.async
import kotlinx.coroutines.coroutineScope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The profile's About and Reposts tabs (2026-10-24, the web's AboutTab and
 * RepostsTab), on anyone's profile:
 *
 *     GET /v1/profiles/:userId/about    profile-service handler.go:152 (GetAllAbout :688-712):
 *                                       the PUBLIC rows only, grouped `{"<section>": [{section,item_id,data,sort_order}]}`
 *     GET /v1/profiles/:userId/links    handler.go:151 (GetUserLinks): `[{platform,url,display_label,sort_order}]`,
 *                                       unsafe schemes already dropped (public_surface.go:148)
 *     GET /v1/creators/:id/reposts      post-service handler.go:3407 (ListCreatorReposts :3567-3600):
 *                                       `{"items":[{repost_id,type,quote_text,original_post_id,created_at,post}],"next_cursor"}`;
 *                                       a repost whose original the viewer may not read is left off the page
 */
interface ProfilePanelsApi {

    @GET("v1/profiles/{userId}/about")
    suspend fun about(@Path("userId") userId: String): ApiEnvelope<Map<String, List<AboutItemDto>>>

    @GET("v1/profiles/{userId}/links")
    suspend fun links(@Path("userId") userId: String): ApiEnvelope<List<PublicLinkDto>>

    @GET("v1/creators/{creatorId}/reposts")
    suspend fun reposts(
        @Path("creatorId") creatorId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<CreatorRepostsDto>
}

@Serializable
data class PublicLinkDto(
    val platform: String = "",
    val url: String = "",
    @SerialName("display_label") val displayLabel: String = "",
    @SerialName("sort_order") val sortOrder: Int = 0,
)

@Serializable
data class CreatorRepostsDto(
    val items: List<RepostItemDto> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

/** One `UserRepostItem` (post-service service/post.go:4277-4288). */
@Serializable
data class RepostItemDto(
    @SerialName("repost_id") val repostId: String = "",
    /** `repost` or `quote`. */
    val type: String = "",
    @SerialName("quote_text") val quoteText: String = "",
    @SerialName("original_post_id") val originalPostId: String = "",
    @SerialName("created_at") val createdAt: String = "",
    val post: RepostedPostDto? = null,
)

/** The few `PostDetail` fields a repost row draws. */
@Serializable
data class RepostedPostDto(
    val id: String = "",
    val text: String = "",
    val title: String = "",
    @SerialName("content_type") val contentType: String = "",
    val author: RepostAuthorDto? = null,
)

@Serializable
data class RepostAuthorDto(
    val id: String = "",
    @SerialName("display_name") val displayName: String = "",
    val username: String = "",
)

/** One public link on the About tab. */
data class PublicLink(val label: String, val url: String)

/** One About section's rows, in the server's sort order. */
data class AboutSection(val section: String, val items: List<ProfileAboutItem>)

/** The About tab beyond the profile payload's own fields. */
data class ProfileAbout(val sections: List<AboutSection>, val links: List<PublicLink>)

/** One row of the Reposts tab. */
data class RepostRow(
    val repostId: String,
    /** The text the reposter added (a quote repost); blank for a plain repost. */
    val quoteText: String,
    val postId: String,
    val contentType: String,
    /** The original's title, else its text. */
    val excerpt: String,
    val originalAuthor: String,
    val createdAt: String,
)

/** What the profile's panels read; a seam so their ViewModel is tested against a fake. */
interface ProfilePanelsSource {
    suspend fun about(userId: String): AppResult<ProfileAbout>
    suspend fun reposts(userId: String, cursor: String? = null): AppResult<Paged<RepostRow>>
}

@Singleton
class ProfilePanelsRepository @Inject constructor(
    private val api: ProfilePanelsApi,
    private val errorMapper: ErrorMapper,
) : ProfilePanelsSource {

    /**
     * The about rows and the links, read together. The about rows decide: a
     * failed links read leaves the section without links rather than failing
     * the tab.
     */
    override suspend fun about(userId: String): AppResult<ProfileAbout> = coroutineScope {
        val links = async { listApiCall(errorMapper) { api.links(userId) } }
        val about = apiCall(errorMapper) { api.about(userId) }
        about.map { grouped ->
            ProfileAbout(
                sections = aboutSections(grouped),
                links = (links.await() as? AppResult.Success)?.data.orEmpty().toPublicLinks(),
            )
        }
    }

    override suspend fun reposts(userId: String, cursor: String?): AppResult<Paged<RepostRow>> =
        apiCall(errorMapper) { api.reposts(userId, REPOSTS_PAGE, cursor) }
            .map { page -> Paged(page.items.mapNotNull { it.toRow() }, page.nextCursor?.takeIf { it.isNotBlank() }) }

    private companion object {
        const val REPOSTS_PAGE = 20
    }
}

/** The grouped about map as sections, each sorted, sections in a stable order. Internal so it is unit-tested. */
internal fun aboutSections(grouped: Map<String, List<AboutItemDto>>): List<AboutSection> =
    grouped.entries
        .sortedBy { SECTION_ORDER.indexOf(it.key).takeIf { i -> i >= 0 } ?: SECTION_ORDER.size }
        .mapNotNull { (section, rows) ->
            val items = rows.sortedBy { it.sortOrder }.map { row ->
                ProfileAboutItem(
                    itemId = row.itemId,
                    section = section,
                    title = row.data.text("title"),
                    subtitle = row.data.text("subtitle"),
                    detail = row.data.text("detail"),
                    visibility = row.visibility,
                    sortOrder = row.sortOrder,
                )
            }.filter { it.title.isNotBlank() || it.subtitle.isNotBlank() || it.detail.isNotBlank() }
            items.takeIf { it.isNotEmpty() }?.let { AboutSection(section, it) }
        }

private fun Map<String, kotlinx.serialization.json.JsonElement>.text(key: String): String =
    (this[key] as? JsonPrimitive)?.contentOrNull.orEmpty().trim()

/** Only https links leave the app (the profile-service check is the first gate; this is the second). */
internal fun List<PublicLinkDto>.toPublicLinks(): List<PublicLink> =
    sortedBy { it.sortOrder }
        .filter { it.url.trim().startsWith("https://", ignoreCase = true) }
        .map { PublicLink(label = it.displayLabel.ifBlank { it.platform.ifBlank { it.url } }.trim(), url = it.url.trim()) }

/** A repost row, or null when the server sent no original (it never lists one, but a row without it has nothing to open). */
internal fun RepostItemDto.toRow(): RepostRow? {
    val original = post ?: return null
    val id = original.id.ifBlank { originalPostId }
    if (id.isBlank()) return null
    return RepostRow(
        repostId = repostId,
        quoteText = quoteText.trim(),
        postId = id,
        contentType = original.contentType,
        excerpt = original.title.ifBlank { original.text }.trim(),
        originalAuthor = original.author?.let { it.displayName.ifBlank { it.username } }.orEmpty(),
        createdAt = createdAt,
    )
}

/** The web About tab's order; any other section follows. */
private val SECTION_ORDER = listOf("work", "education", "places", "contact", "relationship", "family", "details")
