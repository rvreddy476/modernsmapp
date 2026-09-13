package com.us.android.core.food.realtime

import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.realtime.RealtimeTokenSource
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Food's [RealtimeTokenSource]: `POST /v1/food/realtime/token`, cached until
 * the SSE client reports it refused.
 *
 * The response carries no expiry (the server signs a 30-minute token), so the
 * cache is invalidated by refusal, not by a timer: an expired token surfaces as
 * a 401 INVALID_TOKEN on reconnect and the client asks with forceRefresh.
 *
 * Not bound to the port in DI on purpose: rider and other domains will have
 * their own issuers, and a single SingletonComponent binding would force one.
 * Callers pass this instance to `SseClient.connect`.
 */
@Singleton
class FoodRealtimeTokenSource @Inject constructor(
    private val repository: FoodRepository,
) : RealtimeTokenSource {

    private val mutex = Mutex()
    private var cached: String? = null

    override suspend fun token(forceRefresh: Boolean): String {
        mutex.withLock {
            val current = cached
            if (!forceRefresh && current != null) return current
            when (val result = repository.realtimeToken()) {
                is FoodResult.Success -> {
                    cached = result.value.token
                    return result.value.token
                }
                is FoodResult.Failure -> {
                    cached = null
                    throw FoodRealtimeTokenException(result.error)
                }
            }
        }
    }
}

class FoodRealtimeTokenException(val error: FoodError) :
    Exception("food realtime token could not be issued: $error")
