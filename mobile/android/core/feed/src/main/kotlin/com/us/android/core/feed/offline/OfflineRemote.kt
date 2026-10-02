package com.us.android.core.feed.offline

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.noContentApiCall
import dagger.Lazy
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The four calls the copy state machine makes, in the app's types. An
 * interface so the machine is tested against answers a test controls.
 */
interface OfflineRemote {
    /** [nowMs] is the device's clock, for an expiry the server did not send. */
    suspend fun grant(postId: String, deviceId: String, nowMs: Long): AppResult<OfflineGrant>

    /** The answers by post id; a post the server did not mention is absent. */
    suspend fun check(deviceId: String, postIds: List<String>): AppResult<Map<String, OfflineCheckAnswer>>

    /** The post ids the server holds copies of for this device. */
    suspend fun held(deviceId: String): AppResult<List<String>>

    suspend fun remove(postId: String, deviceId: String): AppResult<Unit>
}

/**
 * post-service's offline routes, mapped into [AppResult]. Thin, like
 * `VideoLibraryRepository`: the state lives in [OfflineCopies].
 *
 * The API is taken lazily: this is reached from the application's start
 * (unfinished copies resume there), and building Retrofit must not land on
 * the cold-start path for a viewer who has no copies.
 */
@Singleton
class OfflineRepository @Inject constructor(
    private val api: Lazy<OfflineApi>,
    private val errorMapper: ErrorMapper,
    private val urls: MediaUrlResolver,
) : OfflineRemote {

    override suspend fun grant(postId: String, deviceId: String, nowMs: Long): AppResult<OfflineGrant> =
        when (val result = apiCall(errorMapper) { api.get().grant(postId, OfflineDeviceRequest(deviceId)) }) {
            is AppResult.Failure -> result
            // `hlsUrl` is the resolver's "gateway path to absolute address"; the name is historical.
            is AppResult.Success -> result.data.toGrant(resolve = urls::hlsUrl, nowMs = nowMs)
                ?.let { AppResult.Success(it) }
                ?: AppResult.Failure(AppError.Malformed("offline grant without a post id or a media path"))
        }

    /** In batches of [OfflineApi.MAX_CHECK_IDS], the server's bound; one failed batch fails the check. */
    override suspend fun check(
        deviceId: String,
        postIds: List<String>,
    ): AppResult<Map<String, OfflineCheckAnswer>> {
        val answers = mutableMapOf<String, OfflineCheckAnswer>()
        for (batch in postIds.distinct().chunked(OfflineApi.MAX_CHECK_IDS)) {
            when (val result = listApiCall(errorMapper) { api.get().check(OfflineCheckRequest(deviceId, batch)) }) {
                is AppResult.Success -> answers += result.data.toAnswers()
                is AppResult.Failure -> return result
            }
        }
        return AppResult.Success(answers)
    }

    override suspend fun held(deviceId: String): AppResult<List<String>> =
        listApiCall(errorMapper) { api.get().list(deviceId) }.map { it.postIds() }

    override suspend fun remove(postId: String, deviceId: String): AppResult<Unit> =
        noContentApiCall(errorMapper) { api.get().remove(postId, deviceId) }
}

/**
 * Why Save offline was refused, in one line. One wording for every surface,
 * branched on the server's code and never on its message.
 */
fun offlineRefusalMessage(error: AppError): String = when (error) {
    is AppError.NoNetwork -> "You're offline. Connect to save this."
    is AppError.Timeout -> "That took too long. Try again."
    is AppError.NotFound -> "This video is no longer available."
    is AppError.Forbidden -> offlineCodeMessage(error.code)
    is AppError.Unknown -> offlineCodeMessage(error.code)
    is AppError.Server -> offlineCodeMessage(error.code)
    else -> COULD_NOT_SAVE_OFFLINE
}

private fun offlineCodeMessage(code: String?): String = when (code) {
    "OFFLINE_NOT_ALLOWED" -> "The creator hasn't allowed saving this offline."
    "NOT_READY" -> "This video isn't ready to save yet. Try again soon."
    "OFFLINE_LIMIT" -> "You've reached the limit of offline copies. Remove one and try again."
    else -> COULD_NOT_SAVE_OFFLINE
}

const val COULD_NOT_SAVE_OFFLINE = "Couldn't save this offline. Try again."
