package com.us.android.feature.profile.ui

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.MuteSource

/** A recording [MuteSource]: every write is logged as `name(id)` and answers [writeResult]. */
internal class FakeMutes : MuteSource {
    val calls = mutableListOf<String>()
    var writeResult: AppResult<Unit> = AppResult.Success(Unit)

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
