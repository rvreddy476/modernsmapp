package com.us.android.core.feed.data.action

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.JsonElement
import retrofit2.http.GET
import retrofit2.http.Path

/**
 * The Action button's own reads (contract 23 Oct 2026). The writes ride the
 * post create / PATCH, the reel draft PATCH, the composer draft payload and
 * `PATCH /v1/channels/me` — each through its own API.
 *
 *     GET /v1/posts/action-labels      [{value,label}] (public; a bare array or the envelope)
 *     GET /v1/links/:id                {id,url,domain,label,status}; 404 when not active; counts a click
 *     GET /v1/posts/:id/action/stats   {clicks_total, clicks_7d} (author only)
 *     GET /v1/channels/me/action/stats  the same, for the profile button (owner only)
 */
interface ActionApi {

    /** Raw, so a bare array and an enveloped one both read ([ActionRepository.labelsOf]). */
    @GET("v1/posts/action-labels")
    suspend fun labels(): JsonElement

    @GET("v1/links/{linkId}")
    suspend fun link(@Path("linkId") linkId: String): ApiEnvelope<ActionLinkDto>

    @GET("v1/posts/{postId}/action/stats")
    suspend fun stats(@Path("postId") postId: String): ApiEnvelope<ActionStatsDto>

    /** The profile button's taps (2026-10-24): the signed-in owner's channel. */
    @GET("v1/channels/me/action/stats")
    suspend fun profileStats(): ApiEnvelope<ActionStatsDto>
}
