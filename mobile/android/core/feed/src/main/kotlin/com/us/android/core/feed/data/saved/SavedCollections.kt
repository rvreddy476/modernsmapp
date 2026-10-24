package com.us.android.core.feed.data.saved

import com.us.android.core.common.error.AppError
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

/*
 * Saved collections (2026-10-24, server commit 546eec063).
 *
 * Authority: post-service internal/http/saved_collections_handler.go (routes
 * and refusals, lines 17-43 and 63-85), handler.go AddBookmark (:1870-1903)
 * and GetBookmarks' `?collection_id=` (:1948-1956), and
 * service/saved_collections.go (the name rules, :36-79).
 *
 * An item is in exactly ONE collection. The default one ("All Saved", id
 * null) always exists, cannot be renamed or deleted, and its count is the
 * items in no named collection; deleting a named collection moves its items
 * back to it. The Saved tab reads one collection at a time through
 * `GET /v1/posts/bookmarks?collection_id=default|<uuid>`.
 */

/** service/saved_collections.go:23 (store DefaultCollectionName). */
const val DEFAULT_COLLECTION_NAME = "All Saved"

/** service/saved_collections.go:37 MaxSavedCollections. */
const val MAX_SAVED_COLLECTIONS = 50

/** service/saved_collections.go:39 MaxCollectionNameLength, in characters after trimming. */
const val MAX_COLLECTION_NAME_LENGTH = 40

/** The `collection_id` a query or a body carries for the default collection (handler parseCollectionRef). */
const val DEFAULT_COLLECTION_REF = "default"

// ── Wire ───────────────────────────────────────────────────────────────

/** One collection row: service/saved_collections.go:82-90. `id` is null on the default. */
@Serializable
data class SavedCollectionDto(
    val id: String? = null,
    val name: String = "",
    val count: Long = 0,
    @SerialName("is_default") val isDefault: Boolean = false,
)

/** `POST` / `PATCH /v1/saved/collections` (saved_collections_handler.go:142-158). */
@Serializable
data class CollectionNameRequest(val name: String)

/**
 * A save into, or a move to, a collection: `{collection_id: <uuid>|"default"}`
 * (saved_collections_handler.go:98-118). Never null on the wire: the move
 * routes refuse a body without the key (`INVALID_REQUEST`), and "default" is
 * the default collection on every route.
 */
@Serializable
data class CollectionRefRequest(@SerialName("collection_id") val collectionId: String)

/** `DELETE /v1/saved/collections/:id` → `{deleted, collection_id, moved_to_default}` (saved_collections_handler.go:249). */
@Serializable
data class DeletedCollectionDto(
    val deleted: Boolean = false,
    @SerialName("collection_id") val collectionId: String? = null,
    @SerialName("moved_to_default") val movedToDefault: Long = 0,
)

/** The move's answer, a saved item (store/postgres/saved.go:17-27); only the collection is read. */
@Serializable
data class SavedItemDto(
    val id: String = "",
    @SerialName("target_id") val targetId: String = "",
    @SerialName("collection_id") val collectionId: String? = null,
)

/** `POST /v1/posts/:id/bookmark` → `{"bookmarked":true}` (handler.go:1902). */
@Serializable
data class BookmarkedDto(val bookmarked: Boolean = false)

interface SavedCollectionsApi {
    @GET("v1/saved/collections")
    suspend fun collections(): ApiEnvelope<List<SavedCollectionDto>>

    @POST("v1/saved/collections")
    suspend fun create(@Body body: CollectionNameRequest): ApiEnvelope<SavedCollectionDto>

    @PATCH("v1/saved/collections/{collectionId}")
    suspend fun rename(
        @Path("collectionId") collectionId: String,
        @Body body: CollectionNameRequest,
    ): ApiEnvelope<SavedCollectionDto>

    @DELETE("v1/saved/collections/{collectionId}")
    suspend fun delete(@Path("collectionId") collectionId: String): ApiEnvelope<DeletedCollectionDto>

    /** Saves the post into a collection, or moves it there when it is already saved (the upsert). */
    @POST("v1/posts/{postId}/bookmark")
    suspend fun saveInto(@Path("postId") postId: String, @Body body: CollectionRefRequest): ApiEnvelope<BookmarkedDto>

    /** Moves an already-saved post (`NOT_SAVED` when it is not). */
    @PUT("v1/posts/{postId}/bookmark/collection")
    suspend fun move(@Path("postId") postId: String, @Body body: CollectionRefRequest): ApiEnvelope<SavedItemDto>
}

// ── Domain ─────────────────────────────────────────────────────────────

/** A collection as the Saved tab draws it. [id] null is All Saved. */
data class SavedCollection(val id: String?, val name: String, val count: Long) {
    val isDefault: Boolean get() = id == null
}

/** The default collection with nothing counted yet. */
val EMPTY_DEFAULT_COLLECTION = SavedCollection(id = null, name = DEFAULT_COLLECTION_NAME, count = 0)

/**
 * `GET /v1/saved/collections`, as the tabs. All Saved comes first and exactly
 * once — the server sends it first (service/saved_collections.go:134), and a
 * body without it still gets one, so the tab is never missing. Rows with no
 * name, repeated ids, and a negative count (read as 0) are cleaned up.
 */
fun List<SavedCollectionDto>.toCollections(): List<SavedCollection> {
    var default: SavedCollection? = null
    val named = mutableListOf<SavedCollection>()
    val seen = mutableSetOf<String>()
    for (row in this) {
        val id = row.id?.takeIf { it.isNotBlank() }
        val count = row.count.coerceAtLeast(0)
        if (row.isDefault || id == null) {
            if (default == null) default = SavedCollection(null, DEFAULT_COLLECTION_NAME, count)
            continue
        }
        if (row.name.isBlank() || !seen.add(id)) continue
        named += SavedCollection(id, row.name, count)
    }
    return listOf(default ?: EMPTY_DEFAULT_COLLECTION) + named
}

fun SavedCollectionDto.toCollection(): SavedCollection =
    SavedCollection(id?.takeIf { it.isNotBlank() && !isDefault }, name.ifBlank { DEFAULT_COLLECTION_NAME }, count.coerceAtLeast(0))

/** The `collection_id` for a collection: "default" for All Saved, else its id. */
fun collectionRef(collectionId: String?): String = collectionId ?: DEFAULT_COLLECTION_REF

/**
 * How "Save to collection" reaches the server (the web's
 * `saveToCollectionRequest`, savedCollections.ts):
 *
 *  - a named collection: `POST /v1/posts/:id/bookmark {collection_id}` — it
 *    saves the post, or moves it there when it was already saved;
 *  - All Saved, not saved yet: the same POST with "default", which the
 *    server reads as the plain save;
 *  - All Saved, already saved: a plain save keeps the post where it is
 *    (handler.go:1882-1884), so this is the move,
 *    `PUT /v1/posts/:id/bookmark/collection {collection_id:"default"}`.
 */
sealed interface SaveToCollectionCall {
    val body: CollectionRefRequest

    data class SaveInto(override val body: CollectionRefRequest) : SaveToCollectionCall
    data class MoveTo(override val body: CollectionRefRequest) : SaveToCollectionCall
}

fun saveToCollectionCall(toCollectionId: String?, alreadySaved: Boolean): SaveToCollectionCall {
    val body = CollectionRefRequest(collectionRef(toCollectionId))
    return if (toCollectionId == null && alreadySaved) {
        SaveToCollectionCall.MoveTo(body)
    } else {
        SaveToCollectionCall.SaveInto(body)
    }
}

// ── Names ──────────────────────────────────────────────────────────────

/** Why a collection name would be refused before it is sent. */
enum class CollectionNameProblem(val message: String) {
    EMPTY("Give the collection a name."),
    TOO_LONG("A collection name can be at most $MAX_COLLECTION_NAME_LENGTH characters."),
    INVALID("That name has characters a collection name can't use."),
    RESERVED("“$DEFAULT_COLLECTION_NAME” is where everything you save starts, so pick another name."),
    TAKEN("You already have a collection with that name."),
    LIMIT("You can have up to $MAX_SAVED_COLLECTIONS collections. Delete one to make another."),
}

/** A name check's answer: the name as the server would store it, or why not. */
sealed interface CollectionNameCheck {
    data class Ok(val name: String) : CollectionNameCheck
    data class Refused(val problem: CollectionNameProblem) : CollectionNameCheck
}

private val RESERVED_NAMES = setOf("all saved", "default")

/** Trims and folds inner whitespace runs to one space, as service/saved_collections.go:70 stores it. */
fun normalizeCollectionName(raw: String): String = raw.trim().split(Regex("\\s+")).filter { it.isNotEmpty() }.joinToString(" ")

/** Go's `unicode.IsControl(r) && !unicode.IsSpace(r)` (service/saved_collections.go:65-69). */
private fun isRefusedControl(codePoint: Int): Boolean =
    Character.getType(codePoint) == Character.CONTROL.toInt() && codePoint !in GO_CONTROL_SPACES

/** The control characters Go's `unicode.IsSpace` counts as space: \t \n \v \f \r and U+0085. */
private val GO_CONTROL_SPACES = setOf(0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x85)

/**
 * The server's name rules (service/saved_collections.go:62-79) plus the two
 * the store enforces — unique per user case-insensitively, at most 50.
 * [renamingId] is the collection being renamed: its own name is not
 * "taken", and the limit does not apply to a rename.
 */
fun collectionNameCheck(
    raw: String,
    collections: List<SavedCollection>,
    renamingId: String? = null,
): CollectionNameCheck {
    if (raw.codePoints().anyMatch(::isRefusedControl)) return CollectionNameCheck.Refused(CollectionNameProblem.INVALID)
    val name = normalizeCollectionName(raw)
    val length = name.codePointCount(0, name.length)
    val problem = when {
        length == 0 -> CollectionNameProblem.EMPTY
        length > MAX_COLLECTION_NAME_LENGTH -> CollectionNameProblem.TOO_LONG
        name.lowercase() in RESERVED_NAMES -> CollectionNameProblem.RESERVED
        collections.any { !it.isDefault && it.id != renamingId && it.name.equals(name, ignoreCase = true) } ->
            CollectionNameProblem.TAKEN
        renamingId == null && !canCreateCollection(collections) -> CollectionNameProblem.LIMIT
        else -> null
    }
    return if (problem == null) CollectionNameCheck.Ok(name) else CollectionNameCheck.Refused(problem)
}

/** Whether another collection may be created (the default does not count). */
fun canCreateCollection(collections: List<SavedCollection>): Boolean =
    collections.count { !it.isDefault } < MAX_SAVED_COLLECTIONS

// ── Move, delete and the tab on screen ─────────────────────────────────

/** The counts after a post moved from one collection to another; a same-collection move changes nothing. */
fun List<SavedCollection>.afterMove(fromCollectionId: String?, toCollectionId: String?): List<SavedCollection> {
    if (fromCollectionId == toCollectionId) return this
    return map { collection ->
        when (collection.id) {
            fromCollectionId -> collection.copy(count = (collection.count - 1).coerceAtLeast(0))
            toCollectionId -> collection.copy(count = collection.count + 1)
            else -> collection
        }
    }
}

/** The counts after a post was saved straight into [toCollectionId] (it was in no collection before). */
fun List<SavedCollection>.afterSave(toCollectionId: String?): List<SavedCollection> =
    map { if (it.id == toCollectionId) it.copy(count = it.count + 1) else it }

/** After deleting a collection: it goes, and its items are counted in All Saved. */
fun List<SavedCollection>.afterDelete(deletedId: String, movedToDefault: Long): List<SavedCollection> =
    filter { it.id != deletedId }.map { if (it.isDefault) it.copy(count = it.count + movedToDefault.coerceAtLeast(0)) else it }

/** After a rename: the row with the server's stored name. */
fun List<SavedCollection>.afterRename(renamed: SavedCollection): List<SavedCollection> =
    map { if (it.id != null && it.id == renamed.id) it.copy(name = renamed.name) else it }

/** The collection to show: the chosen one while it still exists, else All Saved (null). */
fun resolveSelectedCollection(selected: String?, collections: List<SavedCollection>): String? =
    selected?.takeIf { id -> collections.any { it.id == id } }

/** Whether a moved post still belongs on the collection on screen. */
fun stillShownAfterMove(onScreenCollectionId: String?, toCollectionId: String?): Boolean =
    onScreenCollectionId == toCollectionId

// ── Refusals ───────────────────────────────────────────────────────────

/** What the viewer was doing when a saved-collections call failed. */
enum class SavedAction(val fallback: String) {
    LOAD("Your collections couldn't load. Try again."),
    CREATE("That collection couldn't be created. Try again."),
    RENAME("That collection couldn't be renamed. Try again."),
    DELETE("That collection couldn't be deleted. Try again."),
    MOVE("That post couldn't be moved. Try again."),
    SAVE("That post couldn't be saved. Try again."),
}

private const val COLLECTION_GONE = "That collection no longer exists. It may have been deleted on another device."

/** The refusal codes of writeSavedCollectionError (saved_collections_handler.go:63-85), as sentences. */
private val SAVED_REFUSALS: Map<String, String> = mapOf(
    "INVALID_COLLECTION_NAME" to "A collection name needs 1 to $MAX_COLLECTION_NAME_LENGTH characters " +
        "and can't be “$DEFAULT_COLLECTION_NAME”.",
    "COLLECTION_NAME_TAKEN" to CollectionNameProblem.TAKEN.message,
    "COLLECTION_LIMIT_REACHED" to CollectionNameProblem.LIMIT.message,
    "COLLECTION_NOT_FOUND" to COLLECTION_GONE,
    "INVALID_COLLECTION_ID" to COLLECTION_GONE,
    "NOT_SAVED" to "This post isn't in your saved items any more.",
    "STORE_UNAVAILABLE" to "Saved items are unavailable right now. Try again in a moment.",
)

/** The server's code on any error shape that carries one (404s included, since 2026-10-24). */
internal fun AppError.savedCode(): String? = when (this) {
    is AppError.Unknown -> code
    is AppError.Server -> code
    is AppError.NotFound -> code
    is AppError.Forbidden -> code
    is AppError.AuthFailed -> code
    else -> null
}

/** A failed saved-collections call, in one sentence. */
fun savedCollectionRefusal(error: AppError, action: SavedAction): String {
    error.savedCode()?.let { code -> SAVED_REFUSALS[code]?.let { return it } }
    return when (error) {
        is AppError.NoNetwork, is AppError.Timeout -> "You seem to be offline. Check your connection and try again."
        is AppError.AuthFailed -> "Log in again to manage your saved items."
        is AppError.RateLimited -> "Too many changes at once. Wait a moment and try again."
        else -> action.fallback
    }
}
