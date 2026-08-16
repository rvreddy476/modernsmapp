package com.us.android.core.common.result

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import org.junit.Test

class AppResultTest {

    @Test
    fun `map transforms success payload`() {
        val result: AppResult<Int> = AppResult.Success(21)
        assertThat(result.map { it * 2 }).isEqualTo(AppResult.Success(42))
    }

    @Test
    fun `map leaves failure untouched`() {
        val error = AppError.Timeout(requestId = "req-1")
        val result: AppResult<Int> = AppResult.Failure(error)

        val mapped = result.map { it * 2 }

        assertThat(mapped).isInstanceOf(AppResult.Failure::class.java)
        assertThat((mapped as AppResult.Failure).error).isSameInstanceAs(error)
    }

    @Test
    fun `map does not invoke the transform on failure`() {
        var invoked = false
        val result: AppResult<Int> = AppResult.Failure(AppError.NoNetwork())

        result.map {
            invoked = true
            it
        }

        assertThat(invoked).isFalse()
    }

    @Test
    fun `onSuccess and onFailure fire only on the matching branch`() {
        var successHits = 0
        var failureHits = 0

        AppResult.Success("ok")
            .onSuccess { successHits++ }
            .onFailure { failureHits++ }

        AppResult.Failure(AppError.AuthFailed())
            .onSuccess { successHits++ }
            .onFailure { failureHits++ }

        assertThat(successHits).isEqualTo(1)
        assertThat(failureHits).isEqualTo(1)
    }

    @Test
    fun `getOrNull unwraps success and nulls failure`() {
        val success: AppResult<String> = AppResult.Success("value")
        // Typed explicitly: AppResult.Failure is AppResult<Nothing>, so
        // getOrNull() would return Nothing? and leave Truth's assertThat
        // overloads ambiguous.
        val failure: AppResult<String> = AppResult.Failure(AppError.NotFound())

        assertThat(success.getOrNull()).isEqualTo("value")
        assertThat(failure.getOrNull()).isNull()
    }

    @Test
    fun `errors carry the request id for backend correlation`() {
        // meta.request_id is the only thread between a user-reported failure
        // and the backend log line that explains it, so every error case
        // must be able to hold it.
        val errors: List<AppError> = listOf(
            AppError.NoNetwork(requestId = "r1"),
            AppError.Timeout(requestId = "r1"),
            AppError.Malformed(detail = "bad json", requestId = "r1"),
            AppError.InvalidRequest(message = "nope", requestId = "r1"),
            AppError.AuthFailed(requestId = "r1"),
            AppError.Forbidden(code = "EMAIL_NOT_VERIFIED", requestId = "r1"),
            AppError.NotFound(requestId = "r1"),
            AppError.RateLimited(retryAfterSeconds = 30, requestId = "r1"),
            AppError.Server(statusCode = 500, code = null, requestId = "r1"),
            AppError.Unknown(code = "WAT", statusCode = 418, requestId = "r1"),
        )

        assertThat(errors.map { it.requestId }.distinct()).containsExactly("r1")
    }
}
