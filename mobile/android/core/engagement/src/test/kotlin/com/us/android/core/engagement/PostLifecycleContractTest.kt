package com.us.android.core.engagement

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.engagement.data.DeletedMediaDto
import com.us.android.core.engagement.data.DeletedPostDto
import com.us.android.core.engagement.data.PostLifecycleApi
import com.us.android.core.engagement.data.PostLifecycleRepository
import com.us.android.core.engagement.data.RestoreOutcome
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * The soft-delete lifecycle against the contract as the server agent is
 * finishing it (2026-09-04): a 204 delete is a success with no body, a 410
 * or a coded 403 on restore is "the window has passed", and the deleted
 * list folds `meta.next_cursor` and picks each row's still.
 */
class PostLifecycleContractTest {

    private val mapper = ErrorMapper(Json { ignoreUnknownKeys = true })

    private class FakeApi : PostLifecycleApi {
        var restoreStatus: Int? = null
        var restoreCode: String = "RESTORE_WINDOW_PASSED"
        var page: List<DeletedPostDto> = emptyList()
        var nextCursor: String? = null
        val listCalls = mutableListOf<Pair<String?, Int>>()

        override suspend fun deletePost(postId: String) = Unit

        override suspend fun restorePost(postId: String): ApiEnvelope<DeletedPostDto> {
            restoreStatus?.let { status ->
                throw HttpException(
                    Response.error<Any>(
                        status,
                        """{"error":{"code":"$restoreCode","message":"no"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
            return ApiEnvelope(data = DeletedPostDto(id = postId, text = "back", deletedAt = "", purgeAt = ""))
        }

        override suspend fun listDeleted(cursor: String?, limit: Int): ApiEnvelope<List<DeletedPostDto>> {
            listCalls += cursor to limit
            return ApiEnvelope(data = page, meta = ApiMeta(nextCursor = nextCursor))
        }
    }

    @Test
    fun `a 204 delete is a success with no body`() = runTest {
        val repository = PostLifecycleRepository(FakeApi(), mapper)

        assertThat(repository.deletePost("p1")).isEqualTo(AppResult.Success(Unit))
    }

    @Test
    fun `restore returns the post`() = runTest {
        val repository = PostLifecycleRepository(FakeApi(), mapper)

        val outcome = repository.restorePost("p1")

        assertThat(outcome).isInstanceOf(RestoreOutcome.Restored::class.java)
        assertThat((outcome as RestoreOutcome.Restored).post.id).isEqualTo("p1")
    }

    @Test
    fun `a 410 or a coded 403 on restore is the window having passed`() = runTest {
        val api = FakeApi()
        val repository = PostLifecycleRepository(api, mapper)

        api.restoreStatus = 410
        assertThat(repository.restorePost("p1")).isEqualTo(RestoreOutcome.WindowPassed)

        api.restoreStatus = 403
        assertThat(repository.restorePost("p1")).isEqualTo(RestoreOutcome.WindowPassed)
    }

    @Test
    fun `any other refusal on restore is a failure with its error`() = runTest {
        val api = FakeApi().apply { restoreStatus = 500 }
        val repository = PostLifecycleRepository(api, mapper)

        val outcome = repository.restorePost("p1")

        assertThat(outcome).isInstanceOf(RestoreOutcome.Failed::class.java)
        assertThat((outcome as RestoreOutcome.Failed).error).isInstanceOf(AppError.Server::class.java)
    }

    @Test
    fun `the deleted list folds the cursor and asks for the page size`() = runTest {
        val api = FakeApi().apply {
            page = listOf(DeletedPostDto(id = "p1"), DeletedPostDto(id = "p2"))
            nextCursor = "c2"
        }
        val repository = PostLifecycleRepository(api, mapper)

        val result = repository.listDeleted(cursor = null) as AppResult.Success

        assertThat(result.data.items.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(result.data.nextCursor).isEqualTo("c2")
        assertThat(api.listCalls).containsExactly(null to PostLifecycleRepository.PAGE_SIZE)
    }

    @Test
    fun `a row's still is the thumbnail, else the smallest sized variant, else whatever was signed`() = runTest {
        val api = FakeApi().apply {
            page = listOf(
                DeletedPostDto(
                    id = "thumb",
                    media = listOf(DeletedMediaDto(variants = mapOf("720p" to "u720", "thumb_150" to "uthumb"))),
                ),
                DeletedPostDto(
                    id = "sized",
                    media = listOf(DeletedMediaDto(variants = mapOf("1080p" to "u1080", "360p" to "u360"))),
                ),
                DeletedPostDto(
                    id = "original",
                    media = listOf(DeletedMediaDto(variants = mapOf("original" to "uorig"))),
                ),
                DeletedPostDto(id = "text"),
            )
        }
        val repository = PostLifecycleRepository(api, mapper)

        val page = repository.listDeleted(cursor = null) as AppResult.Success
        val rows = page.data.items.associate { it.id to it.thumbnailUrl }

        assertThat(rows).containsExactly("thumb", "uthumb", "sized", "u360", "original", "uorig", "text", null)
    }
}
