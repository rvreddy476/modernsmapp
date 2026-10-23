package com.us.android.core.feed.data.action

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.model.ActionButtons
import com.us.android.core.model.ActionLabel
import com.us.android.core.model.ActionLinks
import com.us.android.core.model.ActionStats
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.CancellationException
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import javax.inject.Inject
import javax.inject.Singleton

/** What `GET /v1/links/:id` answered, as the opener acts on it. */
sealed interface ActionLinkResult {
    /** An active https link: open it (through the leaving page the first time). */
    data class Ready(val target: ActionLinks.Target) : ActionLinkResult

    /** 404 / 410, or nothing we may open: "This link is no longer available". */
    data object Gone : ActionLinkResult

    /** Offline or the server failed: say so, offer to try again. */
    data class Failed(val message: String) : ActionLinkResult
}

/**
 * The Action button's reads: the label list (cached for the process once
 * read; the contract's list whenever it cannot be), the link a tap opens,
 * and the creator's tap counts.
 */
@Singleton
class ActionRepository @Inject constructor(
    private val api: ActionApi,
    private val errorMapper: ErrorMapper,
) {
    @Volatile
    private var cachedLabels: List<ActionLabel>? = null

    /** The picker's options. Never empty and never fails: a failed read is the contract's list (not cached, so the next asks again). */
    @Suppress("TooGenericExceptionCaught") // A failed public read degrades to the contract's list, whatever the cause.
    suspend fun labels(): List<ActionLabel> {
        cachedLabels?.let { return it }
        return try {
            labelsOf(api.labels()).also { if (it !== ActionButtons.LABELS) cachedLabels = it }
        } catch (e: CancellationException) {
            throw e
        } catch (_: Throwable) {
            ActionButtons.LABELS
        }
    }

    /** The target behind a button. Counts a click on the server. */
    suspend fun link(linkId: String): ActionLinkResult {
        val id = linkId.trim()
        if (id.isEmpty()) return ActionLinkResult.Gone
        return when (val result = apiCall(errorMapper) { api.link(id) }) {
            is AppResult.Success -> ActionLinks.targetOf(result.data.id.ifBlank { id }, result.data.url, result.data.label, result.data.status)
                ?.let { ActionLinkResult.Ready(it) } ?: ActionLinkResult.Gone
            is AppResult.Failure -> when {
                result.error.isGone() -> ActionLinkResult.Gone
                result.error is AppError.NoNetwork || result.error is AppError.Timeout ->
                    ActionLinkResult.Failed("You're offline. Check your connection and try again.")
                else -> ActionLinkResult.Failed("We couldn't open this link. Try again.")
            }
        }
    }

    /** Taps on [postId]'s button; null when they could not be read (no button, not the author, offline). */
    suspend fun stats(postId: String): ActionStats? = statsOf(apiCall(errorMapper) { api.stats(postId) })

    /** Taps on the signed-in owner's profile button; null when they could not be read. */
    suspend fun profileStats(): ActionStats? = statsOf(apiCall(errorMapper) { api.profileStats() })

    private fun statsOf(result: AppResult<ActionStatsDto>): ActionStats? = when (result) {
        is AppResult.Success -> ActionStats(result.data.clicksTotal.coerceAtLeast(0), result.data.clicks7d.coerceAtLeast(0))
        is AppResult.Failure -> null
    }

    companion object {
        private const val HTTP_GONE = 410

        private fun AppError.isGone(): Boolean =
            this is AppError.NotFound || (this is AppError.Unknown && statusCode == HTTP_GONE)

        /**
         * The labels body → options: a bare array or `{data: [...]}`; rows
         * without a value dropped. Nothing usable is the contract's list.
         */
        fun labelsOf(body: JsonElement): List<ActionLabel> {
            val list = when (body) {
                is JsonArray -> body
                is JsonObject -> body["data"] as? JsonArray
                else -> null
            } ?: return ActionButtons.LABELS
            val rows = list.map { row ->
                val o = row as? JsonObject
                o?.string("value") to o?.string("label")
            }
            return ActionButtons.labelsFrom(rows)
        }

        private fun JsonObject.string(key: String): String? = (this[key] as? JsonPrimitive)?.takeIf { it.isString }?.content
    }
}
