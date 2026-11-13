package com.us.android.feature.premium

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.feature.premium.data.InvoiceKind
import com.us.android.feature.premium.data.PassStatus
import com.us.android.feature.premium.data.PremiumApi
import com.us.android.feature.premium.data.PremiumRepository
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.jsonObject
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.ByteArrayOutputStream
import java.io.File

/**
 * Momentum Premium's wire, both ways, through the real Retrofit stack and the
 * platform Json. The bodies are monetization-service's golden fixtures
 * (testdata/contracts/premium), copied byte for byte.
 */
class PremiumContractTest {

    private lateinit var server: MockWebServer
    private lateinit var repository: PremiumRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(testJson.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(PremiumApi::class.java)
        repository = PremiumRepository(api, ErrorMapper(testJson))
    }

    @After
    fun tearDown() = server.close()

    private fun answer(name: String, code: Int = 200) =
        server.enqueue(MockResponse.Builder().code(code).body(fixture(name)).build())

    private fun refuse(code: Int, errorCode: String) = server.enqueue(
        MockResponse.Builder().code(code).body("""{"error":{"code":"$errorCode","message":"x"}}""").build(),
    )

    private fun <T> AppResult<T>.ok(): T = (this as AppResult.Success).data

    @Test
    fun `the fixtures in this module are the service's own, byte for byte`() {
        val service = File("../../../../Architecture/services/monetization-service/testdata/contracts/premium")
        if (!service.isDirectory) return // a tree without the backend: the copy was checked when it was made
        service.listFiles()!!.filter { it.extension == "json" }.forEach { golden ->
            val copy = File("src/test/resources/contracts/premium/${golden.name}")
            assertThat(copy.readBytes()).isEqualTo(golden.readBytes())
        }
    }

    @Test
    fun `plans parse with the server's GST split`() {
        answer("plans.json")
        val plans = runBlocking { repository.plans() }.ok()

        assertThat(plans.map { it.code }).containsExactly("premium_30d", "premium_365d").inOrder()
        val month = plans[0]
        assertThat(month.durationDays).isEqualTo(30)
        assertThat(month.pricePaise).isEqualTo(14_900)
        assertThat(month.tax!!.taxablePaise).isEqualTo(14_900)
        assertThat(month.tax!!.gstPaise).isEqualTo(2_682)
        assertThat(month.tax!!.totalPaise).isEqualTo(17_582)
        assertThat(month.tax!!.rateBp).isEqualTo(1_800)
        assertThat(month.tax!!.provisional).isTrue()
        assertThat(month.available).isTrue()
        assertThat(plans[1].tax!!.totalPaise).isEqualTo(176_882)
        assertThat(server.takeRequest().target).isEqualTo("/v1/monetization/premium/plans")
    }

    @Test
    fun `me parses the status`() {
        answer("me.json")
        val me = runBlocking { repository.me() }.ok()

        assertThat(me.active).isTrue()
        assertThat(me.inGrace).isFalse()
        assertThat(me.endsAt).isEqualTo("2026-12-13T10:00:00Z")
        assertThat(me.graceEndsAt).isEqualTo("2026-12-16T10:00:00Z")
    }

    @Test
    fun `checkout parses the pass, the server total and the client session`() {
        answer("checkout.json", code = 201)
        val checkout = runBlocking { repository.checkout("premium_30d") }.ok()

        assertThat(checkout.passId).isEqualTo("b2222222-2222-4222-8222-222222222222")
        assertThat(checkout.amountPaise).isEqualTo(17_582)
        assertThat(checkout.tax!!.totalPaise).isEqualTo(17_582)
        assertThat(checkout.currency).isEqualTo("INR")
        assertThat(checkout.clientSession).containsExactlyEntriesIn(
            mapOf("provider" to "razorpay", "order_id" to "order_b2222222", "key_id" to "rzp_test_key"),
        )
    }

    @Test
    fun `the checkout body is exactly the plan code — never an amount`() {
        answer("checkout.json", code = 201)
        runBlocking { repository.checkout("premium_30d") }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/monetization/premium/checkout")
        val body = request.body!!.utf8()
        assertThat(body).isEqualTo("""{"plan":"premium_30d"}""")
        assertThat(testJson.parseToJsonElement(body).jsonObject)
            .isEqualTo(JsonObject(mapOf("plan" to JsonPrimitive("premium_30d"))))
        assertThat(body).doesNotContain("amount")
        assertThat(body).doesNotContain("paise")
    }

    @Test
    fun `a paid pass parses with its end, receipt and invoice`() {
        answer("pass.json")
        val pass = runBlocking { repository.pass("b2222222-2222-4222-8222-222222222222") }.ok()

        assertThat(pass.status).isEqualTo(PassStatus.PAID)
        assertThat(pass.endsAt).isEqualTo("2026-12-13T10:00:00Z")
        assertThat(pass.invoiceId).isEqualTo("d4444444-4444-4444-8444-444444444444")
        assertThat(pass.receipt!!.amountPaise).isEqualTo(17_582)
        assertThat(pass.receipt!!.tax!!.gstPaise).isEqualTo(2_682)
        assertThat(server.takeRequest().target)
            .isEqualTo("/v1/monetization/premium/passes/b2222222-2222-4222-8222-222222222222")
    }

    @Test
    fun `invoices, an invoice and a credit note parse`() {
        answer("invoices.json")
        answer("invoice.json")
        answer("credit_note.json")

        val rows = runBlocking { repository.invoices() }.ok()
        assertThat(rows.single().number).isEqualTo("MP/26-27/000001")
        assertThat(rows.single().kind).isEqualTo(InvoiceKind.INVOICE)
        assertThat(rows.single().totalPaise).isEqualTo(17_582)

        val invoice = runBlocking { repository.invoice("d4444444-4444-4444-8444-444444444444") }.ok()!!
        assertThat(invoice.invoice.kind).isEqualTo(InvoiceKind.INVOICE)
        assertThat(invoice.buyerName).isEqualTo("Asha Rao")
        assertThat(invoice.originalNumber).isNull()

        val note = runBlocking { repository.invoice("c9999999-9999-4999-8999-999999999999") }.ok()!!
        assertThat(note.invoice.kind).isEqualTo(InvoiceKind.CREDIT_NOTE)
        assertThat(note.invoice.totalPaise).isEqualTo(-17_582)
        assertThat(note.buyerName).isNull()
        assertThat(note.originalNumber).isEqualTo("MP/26-27/000002")
    }

    @Test
    fun `the document streams into the picked file, by id, through the client`() {
        server.enqueue(
            MockResponse.Builder().code(200).addHeader("Content-Type", "text/html")
                .body("<html>MP/26-27/000001</html>").build(),
        )
        val out = ByteArrayOutputStream()
        val written = runBlocking { repository.saveInvoiceDocument("d4444444-4444-4444-8444-444444444444", out) }.ok()

        assertThat(out.toString(Charsets.UTF_8.name())).isEqualTo("<html>MP/26-27/000001</html>")
        assertThat(written).isEqualTo(out.size().toLong())
        val request = server.takeRequest()
        val document = "/v1/monetization/premium/invoices/d4444444-4444-4444-8444-444444444444/document"
        assertThat(request.target).isEqualTo(document)
        assertThat(request.target).doesNotContain("token")
    }

    @Test
    fun `another buyer's document is not found, and nothing is written`() {
        refuse(404, "NOT_FOUND")
        val out = ByteArrayOutputStream()
        val result = runBlocking { repository.saveInvoiceDocument("x", out) }

        assertThat((result as AppResult.Failure).error).isInstanceOf(AppError.NotFound::class.java)
        assertThat(out.size()).isEqualTo(0)
    }

    @Test
    fun `the server's switch off reaches the app as PREMIUM_NOT_ENABLED`() {
        refuse(503, "PREMIUM_NOT_ENABLED")
        val result = runBlocking { repository.plans() }

        val error = (result as AppResult.Failure).error
        assertThat(error).isInstanceOf(AppError.Server::class.java)
        assertThat((error as AppError.Server).code).isEqualTo("PREMIUM_NOT_ENABLED")
    }
}
