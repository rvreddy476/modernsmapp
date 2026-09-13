package com.us.android.core.food.realtime

import com.us.android.core.food.network.RealtimeTokenDto
import com.us.android.core.food.network.RealtimeTokenRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.realtime.RealtimeTokenSource
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The one scope a food realtime token is issued for (food-service B5,
 * handler_realtime.go). Each scope maps to exactly one request body, so the
 * "id required / id refused" rule lives here and nowhere else.
 */
sealed interface FoodRealtimeScope {

    /** The caller's own rider stream: `food.delivery_partner.<user_id>.assignments`. Takes NO id. */
    data object Delivery : FoodRealtimeScope

    /** A restaurant the caller owns: `food.restaurant.<id>.orders` and `food.restaurant.<id>`. */
    data class Restaurant(val restaurantId: String) : FoodRealtimeScope

    /** An order the caller placed: `food.order.<id>`. */
    data class Order(val orderId: String) : FoodRealtimeScope

    fun toRequest(): RealtimeTokenRequest = when (this) {
        Delivery -> RealtimeTokenRequest(scope = "delivery")
        is Restaurant -> RealtimeTokenRequest(scope = "restaurant", id = restaurantId)
        is Order -> RealtimeTokenRequest(scope = "order", id = orderId)
    }
}

/** Issues one token for one request. The seam tests replace. */
fun interface RealtimeTokenIssuer {
    suspend fun issue(request: RealtimeTokenRequest): FoodResult<RealtimeTokenDto>
}

/**
 * Food's [RealtimeTokenSource] for one [scope].
 *
 * NO CACHE, on purpose. The scoped token lives five minutes and
 * notification-service checks it only when a connection opens, so every
 * (re)connect needs a fresh one. `SseClient` asks exactly once per connection
 * attempt, which makes "fetch on every call" the same thing as "fetch on every
 * (re)connect" — and a cached token would turn every reconnect after five
 * minutes into a guaranteed 401 and a wasted round trip. [token]'s
 * `forceRefresh` is therefore always honoured trivially.
 *
 * A failure throws [FoodRealtimeTokenException]; the SSE client backs off on it,
 * and a caller watching the source (the kitchen queue) falls back to polling.
 */
class FoodRealtimeTokenSource(
    private val issuer: RealtimeTokenIssuer,
    val scope: FoodRealtimeScope,
) : RealtimeTokenSource {

    override suspend fun token(forceRefresh: Boolean): String =
        when (val result = issuer.issue(scope.toRequest())) {
            is FoodResult.Success -> result.value.token.takeIf { it.isNotBlank() }
                ?: throw FoodRealtimeTokenException(FoodError.Unexpected(null, "EMPTY_TOKEN", "empty realtime token"))
            is FoodResult.Failure -> throw FoodRealtimeTokenException(result.error)
        }
}

/**
 * Hands out a [RealtimeTokenSource] per scope. Kitchen asks for
 * [FoodRealtimeScope.Restaurant], Rider for [FoodRealtimeScope.Delivery].
 */
@Singleton
class FoodRealtimeTokens(private val issuer: RealtimeTokenIssuer) {

    @Inject
    constructor(repository: FoodRepository) : this(RealtimeTokenIssuer { repository.realtimeToken(it) })

    fun forScope(scope: FoodRealtimeScope): RealtimeTokenSource = FoodRealtimeTokenSource(issuer, scope)
}

class FoodRealtimeTokenException(val error: FoodError) :
    Exception("food realtime token could not be issued: $error")
