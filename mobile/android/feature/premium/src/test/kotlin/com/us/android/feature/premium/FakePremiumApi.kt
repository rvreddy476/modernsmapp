package com.us.android.feature.premium

import android.app.Activity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import com.us.android.feature.premium.data.CheckoutDto
import com.us.android.feature.premium.data.CheckoutRequest
import com.us.android.feature.premium.data.InvoiceDto
import com.us.android.feature.premium.data.InvoicesDto
import com.us.android.feature.premium.data.PassDto
import com.us.android.feature.premium.data.PlansDto
import com.us.android.feature.premium.data.PremiumApi
import com.us.android.feature.premium.data.StatusDto
import kotlinx.serialization.KSerializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.HttpException
import retrofit2.Response
import java.io.File
import java.io.IOException

val testJson = NetworkModule.provideJson()

/** A golden body from monetization-service testdata/contracts/premium (copied byte for byte). */
fun fixture(name: String): String = File("src/test/resources/contracts/premium/$name").readText()

fun <T> envelope(name: String, serializer: KSerializer<T>): ApiEnvelope<T> =
    testJson.decodeFromString(ApiEnvelope.serializer(serializer), fixture(name))

/** A refusal as Retrofit throws it: the status and the platform's error envelope. */
fun refused(status: Int, code: String): Nothing = throw HttpException(
    Response.error<Any>(
        status,
        """{"error":{"code":"$code","message":"m"}}""".toResponseBody("application/json".toMediaType()),
    ),
)

fun offline(): Nothing = throw IOException("offline")

/**
 * A scripted [PremiumApi]: each call takes the next answer from its queue (a
 * thrown answer is a refusal or a dropped connection) and records what it was
 * sent. With a queue empty, plans/me/invoices answer their fixtures and a pass
 * read answers [lastPass].
 */
class FakePremiumApi : PremiumApi {
    var requests = 0
    val checkoutBodies = mutableListOf<CheckoutRequest>()
    val checkoutAnswers = ArrayDeque<() -> ApiEnvelope<CheckoutDto>>()
    val planAnswers = ArrayDeque<() -> ApiEnvelope<PlansDto>>()
    val passAnswers = ArrayDeque<() -> ApiEnvelope<PassDto>>()
    val meAnswers = ArrayDeque<() -> ApiEnvelope<StatusDto>>()
    val invoiceAnswers = ArrayDeque<() -> ApiEnvelope<InvoicesDto>>()
    var lastPass: () -> ApiEnvelope<PassDto> = { passAnswer("confirming") }
    var passReads = 0
    var planReads = 0

    override suspend fun plans(): ApiEnvelope<PlansDto> {
        requests++
        planReads++
        return planAnswers.removeFirstOrNull()?.invoke() ?: envelope("plans.json", PlansDto.serializer())
    }

    override suspend fun checkout(body: CheckoutRequest): ApiEnvelope<CheckoutDto> {
        requests++
        checkoutBodies += body
        return checkoutAnswers.removeFirstOrNull()?.invoke() ?: envelope("checkout.json", CheckoutDto.serializer())
    }

    override suspend fun me(): ApiEnvelope<StatusDto> {
        requests++
        return meAnswers.removeFirstOrNull()?.invoke() ?: envelope("me.json", StatusDto.serializer())
    }

    override suspend fun pass(passId: String): ApiEnvelope<PassDto> {
        requests++
        passReads++
        return (passAnswers.removeFirstOrNull() ?: lastPass).invoke()
    }

    override suspend fun invoices(): ApiEnvelope<InvoicesDto> {
        requests++
        return invoiceAnswers.removeFirstOrNull()?.invoke() ?: envelope("invoices.json", InvoicesDto.serializer())
    }

    override suspend fun invoice(invoiceId: String): ApiEnvelope<InvoiceDto> {
        requests++
        return envelope("invoice.json", InvoiceDto.serializer())
    }

    override suspend fun invoiceDocument(invoiceId: String): Response<ResponseBody> {
        requests++
        return Response.success("<html>invoice</html>".toResponseBody("text/html".toMediaType()))
    }
}

/** The pass fixture with [status], and the invoice id only when [invoiceId] says so. */
fun passAnswer(status: String, invoiceId: String? = null): ApiEnvelope<PassDto> {
    val paid = envelope("pass.json", PassDto.serializer()).data!!
    return ApiEnvelope(data = paid.copy(status = status, invoiceId = invoiceId))
}

/** Records every sheet opening; [outcomes] decides how each ends (none: the sheet stays open). */
class RecordingLauncher : PaymentLauncher {
    val opened = mutableListOf<Pair<PaymentAttempt, PaymentSession>>()
    var pending: ((PaymentOutcome) -> Unit)? = null

    override fun open(
        activity: Activity,
        attempt: PaymentAttempt,
        session: PaymentSession,
        onOutcome: (PaymentOutcome) -> Unit,
    ) {
        opened += attempt to session
        pending = onOutcome
    }

    override fun abandon(attempt: PaymentAttempt) {
        pending = null
    }
}

fun coordinator(launcher: PaymentLauncher = RecordingLauncher()) = PaymentCoordinator(launcher)
