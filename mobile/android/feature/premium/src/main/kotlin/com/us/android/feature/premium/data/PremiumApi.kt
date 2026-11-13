package com.us.android.feature.premium.data

import com.us.android.core.network.ApiEnvelope
import okhttp3.ResponseBody
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Streaming

/**
 * Momentum Premium through the gateway (monetization-service
 * internal/http/premium_handler.go). While the server's PREMIUM_ENABLED is
 * off every route answers 503 `PREMIUM_NOT_ENABLED`. There is no route that
 * confirms a payment: a pass is paid only by payments-service's signed event.
 *
 * Called through the app-wide Retrofit, so every request carries the bearer
 * token in its header — never in a URL.
 */
interface PremiumApi {

    @GET("v1/monetization/premium/plans")
    suspend fun plans(): ApiEnvelope<PlansDto>

    /** `{"plan": code}` and nothing else (DisallowUnknownFields; an amount is a 400). */
    @POST("v1/monetization/premium/checkout")
    suspend fun checkout(@Body body: CheckoutRequest): ApiEnvelope<CheckoutDto>

    @GET("v1/monetization/premium/me")
    suspend fun me(): ApiEnvelope<StatusDto>

    /** The poll: confirming | paid | failed | refunded; 404 for anyone else's. */
    @GET("v1/monetization/premium/passes/{passId}")
    suspend fun pass(@Path("passId") passId: String): ApiEnvelope<PassDto>

    @GET("v1/monetization/premium/invoices")
    suspend fun invoices(): ApiEnvelope<InvoicesDto>

    /** One document (JSON); 404 for anyone else's, never 403. */
    @GET("v1/monetization/premium/invoices/{invoiceId}")
    suspend fun invoice(@Path("invoiceId") invoiceId: String): ApiEnvelope<InvoiceDto>

    /**
     * The printable HTML (`Content-Disposition: attachment`, `no-store`). Not
     * an envelope: the body IS the document. Streamed straight into the file
     * the buyer picked.
     */
    @Streaming
    @GET("v1/monetization/premium/invoices/{invoiceId}/document")
    suspend fun invoiceDocument(@Path("invoiceId") invoiceId: String): Response<ResponseBody>
}
