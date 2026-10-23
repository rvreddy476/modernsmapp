package com.us.android.core.feed.data.credits

import com.us.android.core.model.CreditStatus
import com.us.android.core.model.CreditedUser
import com.us.android.core.model.VideoCredit
import com.us.android.core.model.VideoCredits
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonTransformingSerializer

/*
 * Video credits on the wire (contract pinned 2026-10-23). Every read field
 * defaults, so a row from a server that predates credits — or a viewer who is
 * shown less than the owner — decodes to "nothing credited" rather than failing.
 *
 * `credits` is on `GET /v1/posts/:id` and the create / PATCH answers only;
 * `genres`, `release_year` and `production_house` are on every post body
 * and left off when unset.
 */

/**
 * A list that reads anything but an array — `""`, `null`, an object — as
 * empty, so a Go zero value or a stray scalar in `credits` / `genres` is "no
 * credits" rather than a post that fails to decode (and drops out of a list).
 */
open class LenientListSerializer<T>(element: KSerializer<T>) :
    JsonTransformingSerializer<List<T>>(ListSerializer(element)) {
    override fun transformDeserialize(element: JsonElement): JsonElement = element as? JsonArray ?: JsonArray(emptyList())
}

/** `credits` on a post read: an array, or nothing. */
object CreditListSerializer : LenientListSerializer<CreditDto>(CreditDto.serializer())

/** `genres` on a post read: an array of slugs, or nothing. */
object GenreSlugListSerializer : LenientListSerializer<String>(String.serializer())

/**
 * One credit as `GET /v1/posts/:id` (and any payload carrying long-video
 * detail) answers it. [user] is sent only for an ACCEPTED tag; [status] only
 * to the post's owner; [userId] only to the owner, on every row that has a
 * tag (pending, accepted or declined) — the editor resends it so the tag
 * keeps its status. A viewer never gets `user_id`.
 */
@Serializable
data class CreditDto(
    val id: String = "",
    val name: String = "",
    val role: String = "",
    val user: CreditUserDto? = null,
    val status: String? = null,
    @SerialName("user_id") val userId: String? = null,
)

@Serializable
data class CreditUserDto(
    val id: String = "",
    val username: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
)

/**
 * One credit as the create request and the PATCH send it: `name` 1..80
 * runes, `role` 1..40 runes, `user_id` when the creator linked an account.
 * `user_id` is left off the wire when null (the app's JSON drops nulls).
 */
@Serializable
data class CreditInputDto(
    val name: String,
    val role: String,
    @SerialName("user_id") val userId: String? = null,
)

/** One entry of `GET /v1/posts/genres`. */
@Serializable
data class GenreDto(
    val slug: String = "",
    val label: String = "",
)

/** One of my pending requests — `GET /v1/credits/requests`. */
@Serializable
data class CreditRequestDto(
    val id: String = "",
    val role: String = "",
    val name: String = "",
    val post: CreditRequestPostDto = CreditRequestPostDto(),
    val from: CreditUserDto = CreditUserDto(),
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class CreditRequestPostDto(
    val id: String = "",
    val title: String = "",
    @SerialName("cover_url") val coverUrl: String? = null,
    @SerialName("channel_name") val channelName: String = "",
)

/** What accept and decline answer: `{"status":"accepted"}` / `{"status":"declined"}`. */
@Serializable
data class CreditStatusDto(val status: String = "")

/** The credits block of a long video, as the read DTOs carry it, to the domain. */
fun creditsOf(
    credits: List<CreditDto>,
    genres: List<String>,
    releaseYear: Int?,
    productionHouse: String?,
): VideoCredits = VideoCredits(
    credits = credits.mapNotNull { it.toDomain() },
    genres = genres.map { it.trim() }.filter { it.isNotEmpty() },
    releaseYear = releaseYear?.takeIf { it > 0 },
    productionHouse = productionHouse?.trim().orEmpty(),
)

/** A credit without a name is not a credit: dropped. */
fun CreditDto.toDomain(): VideoCredit? {
    val shown = name.trim()
    if (shown.isEmpty()) return null
    val status = CreditStatus.fromWire(status)
    // The account appears only for an accepted tag, whatever else was sent:
    // a pending or declined link is the plain name on every surface.
    val linked = user?.takeIf { it.id.isNotBlank() && (status == null || status == CreditStatus.ACCEPTED) }
    return VideoCredit(
        id = id,
        name = shown,
        role = role.trim(),
        user = linked?.toDomain(),
        status = status,
        linkedUserId = (userId ?: user?.id)?.takeIf { it.isNotBlank() },
    )
}

fun CreditUserDto.toDomain() = CreditedUser(
    id = id,
    username = username,
    displayName = displayName.ifBlank { username },
    avatarUrl = avatarUrl?.takeIf { it.isNotBlank() },
)
