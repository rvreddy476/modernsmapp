package com.us.android.feature.doorsteppro.realtime

import com.us.android.core.realtime.RealtimeTokenSource
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProResult

/** The professional's realtime topic (asyncapi `doorstep.pro.{user_id}`): offers and job changes. */
object ProTopics {
    private const val PRO_PREFIX = "doorstep.pro."

    fun forPro(userId: String): List<String> = listOf(PRO_PREFIX + userId)
}

/**
 * Doorstep Pro's [RealtimeTokenSource]: `POST /v1/doorstep/pro/realtime/token`
 * — a token for doorstep.pro.<user_id> and the accepted bookings, minted fresh
 * for every (re)connect (the customer feature's DoorstepRealtimeTokenSource,
 * copied). A refusal throws, which the SSE client treats as a failed attempt
 * and [ProLiveFeed] answers by polling. Until lane A4 ships the route, that is
 * exactly what happens: a 404, and polling every 15 s.
 */
class ProRealtimeTokenSource(
    private val repository: DoorstepProRepository,
) : RealtimeTokenSource {

    override suspend fun token(forceRefresh: Boolean): String =
        when (val result = repository.realtimeToken()) {
            is ProResult.Success -> result.value.token.takeIf { it.isNotBlank() }
                ?: throw ProRealtimeTokenException(ProError.Unexpected(null, "empty realtime token"))
            is ProResult.Failure -> throw ProRealtimeTokenException(result.error)
        }
}

class ProRealtimeTokenException(val error: ProError) :
    Exception("doorstep pro realtime token could not be issued: $error")
