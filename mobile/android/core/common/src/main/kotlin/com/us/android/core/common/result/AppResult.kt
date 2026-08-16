package com.us.android.core.common.result

import com.us.android.core.common.error.AppError

/**
 * The return type of every repository call.
 *
 * Deliberately not kotlin.Result: that type erases the error into Throwable
 * and encourages `runCatching`, which swallows CancellationException and
 * breaks structured concurrency. [AppError] is a closed hierarchy the UI can
 * exhaustively `when` over.
 */
sealed interface AppResult<out T> {
    data class Success<out T>(val data: T) : AppResult<T>
    data class Failure(val error: AppError) : AppResult<Nothing>
}

inline fun <T, R> AppResult<T>.map(transform: (T) -> R): AppResult<R> = when (this) {
    is AppResult.Success -> AppResult.Success(transform(data))
    is AppResult.Failure -> this
}

inline fun <T> AppResult<T>.onSuccess(action: (T) -> Unit): AppResult<T> = apply {
    if (this is AppResult.Success) action(data)
}

inline fun <T> AppResult<T>.onFailure(action: (AppError) -> Unit): AppResult<T> = apply {
    if (this is AppResult.Failure) action(error)
}

fun <T> AppResult<T>.getOrNull(): T? = (this as? AppResult.Success)?.data
