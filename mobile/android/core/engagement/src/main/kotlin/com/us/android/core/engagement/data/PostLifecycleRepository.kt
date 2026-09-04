package com.us.android.core.engagement.data

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.noContentApiCall
import com.us.android.core.network.pagedApiCall
import javax.inject.Inject
import javax.inject.Singleton

/**
 * One of the viewer's deleted posts, as "Recently deleted" lists it.
 *
 * [thumbnailUrl] is the smallest still the server signed — enough for a
 * compact row and nothing more; [text] is the first thing to show when there
 * is no picture.
 */
data class DeletedPost(
    val id: String,
    val text: String,
    val postType: String,
    val thumbnailUrl: String?,
    val createdAt: String,
    val deletedAt: String,
    val purgeAt: String,
)

/** What restoring a deleted post came to. */
sealed interface RestoreOutcome {
    data class Restored(val post: DeletedPost) : RestoreOutcome

    /**
     * The 30 days are up: the server answered 410, or 403 with a code. On the
     * viewer's OWN deleted list a 403 cannot mean "not yours", so both read
     * the same way — the post is past saving and the row should go.
     */
    data object WindowPassed : RestoreOutcome

    data class Failed(val error: AppError) : RestoreOutcome
}

/**
 * Soft delete, restore, and the viewer's deleted list — see
 * [PostLifecycleApi] for the contract. Pure transport: hiding the post from
 * the feeds is [HiddenPosts]'s job and the caller's decision.
 */
@Singleton
class PostLifecycleRepository @Inject constructor(
    private val api: PostLifecycleApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun deletePost(postId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.deletePost(postId) }

    suspend fun restorePost(postId: String): RestoreOutcome =
        when (val result = apiCall(errorMapper, { it.toDeletedPost() }) { api.restorePost(postId) }) {
            is AppResult.Success -> RestoreOutcome.Restored(result.data)
            is AppResult.Failure -> when (val error = result.error) {
                is AppError.Forbidden -> RestoreOutcome.WindowPassed
                is AppError.Unknown -> {
                    if (error.statusCode == HTTP_GONE) RestoreOutcome.WindowPassed else RestoreOutcome.Failed(error)
                }
                else -> RestoreOutcome.Failed(error)
            }
        }

    /** One page of the viewer's deleted posts, newest deletion first. */
    suspend fun listDeleted(cursor: String?): AppResult<Paged<DeletedPost>> =
        when (val result = pagedApiCall(errorMapper) { api.listDeleted(cursor, PAGE_SIZE) }) {
            is AppResult.Success -> AppResult.Success(
                Paged(items = result.data.items.map { it.toDeletedPost() }, nextCursor = result.data.nextCursor),
            )
            is AppResult.Failure -> result
        }

    companion object {
        const val PAGE_SIZE = 20
        private const val HTTP_GONE = 410
    }
}

/**
 * The still to draw in a compact row: the generated thumbnail when there is
 * one, else the smallest sized variant, else whatever the server signed.
 */
internal fun DeletedPostDto.toDeletedPost(): DeletedPost = DeletedPost(
    id = id,
    text = text,
    postType = postType,
    thumbnailUrl = media.firstNotNullOfOrNull { it.thumbnailUrl() },
    createdAt = createdAt,
    deletedAt = deletedAt,
    purgeAt = purgeAt,
)

private fun DeletedMediaDto.thumbnailUrl(): String? {
    if (variants.isEmpty()) return null
    variants[THUMBNAIL_KEY]?.let { return it }
    val smallest = variants.entries
        .mapNotNull { (key, url) -> key.takeIf { it.endsWith('p') }?.dropLast(1)?.toIntOrNull()?.let { it to url } }
        .minByOrNull { (height, _) -> height }
    return smallest?.second ?: variants.values.first()
}

private const val THUMBNAIL_KEY = "thumb_150"
