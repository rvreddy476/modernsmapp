package com.us.android.feature.profile.ui

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.ConnectionPerson
import com.us.android.core.profile.data.ConnectionRequestDirection
import com.us.android.core.profile.data.ConnectionRequestPerson
import com.us.android.core.profile.data.ConnectionsSource

/**
 * A recording [ConnectionsSource]: every write is logged as `name(id)` and
 * answers [writeResult]; the reads answer what the test put in.
 */
internal class FakeConnections : ConnectionsSource {
    val calls = mutableListOf<String>()
    var writeResult: AppResult<Unit> = AppResult.Success(Unit)
    var received: AppResult<List<ConnectionRequestPerson>> = AppResult.Success(emptyList())
    var sent: AppResult<List<ConnectionRequestPerson>> = AppResult.Success(emptyList())
    var people: AppResult<List<ConnectionPerson>> = AppResult.Success(emptyList())

    override suspend fun requests(direction: ConnectionRequestDirection): AppResult<List<ConnectionRequestPerson>> {
        calls += "requests($direction)"
        return if (direction == ConnectionRequestDirection.RECEIVED) received else sent
    }

    override suspend fun pendingSenderIds(): AppResult<Set<String>> {
        calls += "pendingSenderIds"
        return (received as? AppResult.Success)?.let { r -> AppResult.Success(r.data.mapTo(mutableSetOf()) { it.userId }) }
            ?: AppResult.Failure(AppError.Unknown(code = "INTERNAL_ERROR", statusCode = 500))
    }

    override suspend fun connections(userId: String, offset: Int, limit: Int): AppResult<List<ConnectionPerson>> {
        calls += "connections($userId)"
        return people
    }

    override suspend fun send(userId: String) = write("send($userId)")
    override suspend fun accept(senderId: String) = write("accept($senderId)")
    override suspend fun decline(senderId: String) = write("decline($senderId)")
    override suspend fun cancel(receiverId: String) = write("cancel($receiverId)")
    override suspend fun remove(userId: String) = write("remove($userId)")
    override suspend fun mute(userId: String) = write("mute($userId)")
    override suspend fun unmute(userId: String) = write("unmute($userId)")

    private fun write(call: String): AppResult<Unit> {
        calls += call
        return writeResult
    }

    companion object {
        val FAILURE: AppResult<Unit> = AppResult.Failure(AppError.Unknown(code = "INTERNAL_ERROR", statusCode = 500, requestId = null))
    }
}

internal fun requestPerson(id: String, name: String = "") =
    ConnectionRequestPerson(userId = id, displayName = name, username = "", avatarMediaId = null, createdAt = "", message = null)

internal fun connectionPerson(id: String, name: String = "") =
    ConnectionPerson(userId = id, displayName = name, username = "", avatarMediaId = null)
