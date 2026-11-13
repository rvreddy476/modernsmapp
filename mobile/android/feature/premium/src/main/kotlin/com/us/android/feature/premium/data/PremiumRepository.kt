package com.us.android.feature.premium.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import retrofit2.HttpException
import java.io.OutputStream
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.cancellation.CancellationException

/**
 * Every Premium read and the one write, as [AppResult]. The screens and the
 * purchase flow go through this; nothing else calls [PremiumApi].
 *
 * There is no "confirm" here, by design: a pass is paid when
 * `GET /passes/:id` says so (payments-service's signed event, server side).
 */
@Singleton
class PremiumRepository @Inject constructor(
    private val api: PremiumApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun plans(): AppResult<List<PremiumPlan>> =
        apiCall(errorMapper, { it.plans.toPlans() }) { api.plans() }

    suspend fun me(): AppResult<PremiumStatus> =
        apiCall(errorMapper, { it.toStatus() }) { api.me() }

    /**
     * Opens (or, within 30 minutes, reopens) the checkout for [planCode]. The
     * body is `{"plan": planCode}` and nothing else: the server prices the
     * pass, and the client never sends an amount.
     */
    suspend fun checkout(planCode: String): AppResult<PremiumCheckout> =
        apiCall(errorMapper, { it.toCheckout() }) { api.checkout(CheckoutRequest(plan = planCode)) }

    suspend fun pass(passId: String): AppResult<PremiumPass> =
        apiCall(errorMapper, { it.toPass() }) { api.pass(passId) }

    suspend fun invoices(): AppResult<List<PremiumInvoice>> =
        apiCall(errorMapper, { it.invoices.toInvoices() }) { api.invoices() }

    suspend fun invoice(invoiceId: String): AppResult<PremiumInvoiceDetail?> =
        apiCall(errorMapper, { it.toDetail() }) { api.invoice(invoiceId) }

    /**
     * Streams the printable document into [output] — the file the buyer picked
     * in the system's "Save to" — and answers the bytes written. The app keeps
     * no copy, and the bearer token rides the request header, never a URL.
     */
    @Suppress("TooGenericExceptionCaught")
    suspend fun saveInvoiceDocument(invoiceId: String, output: OutputStream): AppResult<Long> = try {
        val response = api.invoiceDocument(invoiceId)
        val body = response.body()
        if (!response.isSuccessful || body == null) {
            // The mapper reads (and closes) the error body for its code.
            AppResult.Failure(errorMapper.map(HttpException(response)))
        } else {
            withContext(Dispatchers.IO) {
                body.use { b -> b.byteStream().use { AppResult.Success(it.copyTo(output)) } }
            }
        }
    } catch (e: CancellationException) {
        throw e
    } catch (e: Throwable) {
        AppResult.Failure(errorMapper.map(e))
    }
}
