package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.AdjustStockRequest
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.StartSellingRequest
import com.us.android.core.commerce.network.StockDto
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_DOCUMENT
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.BUSINESS_PAGE_CHECK_UNAVAILABLE_COPY
import com.us.android.feature.commerce.seller.BUSINESS_PAGE_NOT_FOUND_COPY
import com.us.android.feature.commerce.seller.DOCUMENT_NOT_WHAT_IT_SAYS
import com.us.android.feature.commerce.seller.DOCUMENT_PICK_AGAIN
import com.us.android.feature.commerce.seller.DOCUMENT_TOO_LARGE
import com.us.android.feature.commerce.seller.DOCUMENT_WRONG_TYPE
import com.us.android.feature.commerce.seller.DocumentViewModel
import com.us.android.feature.commerce.seller.PayoutViewModel
import com.us.android.feature.commerce.seller.ProductImagesViewModel
import com.us.android.feature.commerce.seller.StartSellingViewModel
import com.us.android.feature.commerce.seller.StockUiState
import com.us.android.feature.commerce.seller.StockViewModel
import com.us.android.feature.commerce.seller.UPLOAD_OFFLINE
import com.us.android.feature.commerce.seller.UPLOAD_SIGNED_OUT
import com.us.android.feature.commerce.seller.UploadStep
import com.us.android.feature.commerce.seller.describeForSeller
import com.us.android.feature.commerce.seller.documentUploadCopy
import com.us.android.feature.commerce.seller.parseRupees
import com.us.android.feature.commerce.seller.productImageUploadCopy
import com.us.android.feature.commerce.seller.waitFor
import io.mockk.coEvery
import io.mockk.coVerify
import io.mockk.mockk
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.Headers.Companion.headersOf
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response
import java.io.IOException

/**
 * Android lane 6, the seller side: stock-adjust idempotency (audit M3), the
 * private KYC document kind and its refusals (M4, M5, 31 Oct addendum),
 * product-photo rate limits (M5), the payout prefill (L5), price parsing
 * (L6) and the business-page onboarding refusals.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SellerLaneSixTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    // ─── M3: stock ───────────────────────────────────────────────────

    private class StockApi : FakeCommerceApi() {
        var total = 10
        val keys = mutableListOf<String?>()
        val answers = ArrayDeque<suspend (String?) -> Response<ApiEnvelope<StockDto>>>()
        var reads = 0

        override suspend fun stock(variantId: String): Response<ApiEnvelope<StockDto>> {
            reads++
            return envelope(level())
        }

        override suspend fun adjustStock(
            variantId: String,
            idempotencyKey: String?,
            body: AdjustStockRequest,
        ): Response<ApiEnvelope<StockDto>> {
            keys += idempotencyKey
            return answers.removeFirstOrNull()?.invoke(idempotencyKey) ?: run {
                total += body.delta
                envelope(level())
            }
        }

        fun level() = StockDto(variantId = "v-1", totalQty = total, reservedQty = 0, available = total)
    }

    private fun stockVm(api: StockApi, saved: SavedStateHandle = SavedStateHandle(mapOf("variantId" to "v-1"))) =
        StockViewModel(CommerceRepository(api), saved)

    private fun StockViewModel.content() = state.value as StockUiState.Content

    private fun StockViewModel.add(amount: String) {
        setRemoving(false)
        setAmount(amount)
        submit()
    }

    @Test
    fun `a stock save that got no answer is resent under the same key`() = runTest(dispatcher) {
        val api = StockApi()
        // The server applied it; the answer was lost.
        api.answers += { _ ->
            api.total += 50
            throw IOException("read timed out")
        }
        val vm = stockVm(api)
        advanceUntilIdle()

        vm.add("50")
        advanceUntilIdle()
        assertThat(vm.content().error).isNotNull()
        assertThat(vm.content().amount).isEqualTo("50")
        // The level shown is re-read from the server, so the seller can see it landed.
        assertThat(vm.content().level.total).isEqualTo(60)

        api.answers += { _ ->
            Response.success(
                ApiEnvelope(data = api.level()),
                headersOf("Idempotent-Replayed", "true"),
            )
        }
        vm.submit()
        advanceUntilIdle()

        assertThat(api.keys).hasSize(2)
        assertThat(api.keys[1]).isEqualTo(api.keys[0])
        assertThat(api.total).isEqualTo(60)
        assertThat(vm.content().notice).isEqualTo("That change was already saved.")
        assertThat(vm.content().lastChange).isNull()
    }

    @Test
    fun `a saved change spends its key, so the next identical edit is new`() = runTest(dispatcher) {
        val api = StockApi()
        val vm = stockVm(api)
        advanceUntilIdle()

        vm.add("5")
        advanceUntilIdle()
        vm.add("5")
        advanceUntilIdle()

        assertThat(api.keys).hasSize(2)
        assertThat(api.keys[1]).isNotEqualTo(api.keys[0])
        assertThat(api.total).isEqualTo(20)
        assertThat(vm.content().lastChange).isEqualTo(5)
    }

    @Test
    fun `a different edit after a lost answer gets a different key`() = runTest(dispatcher) {
        val api = StockApi()
        api.answers += { _ -> throw IOException("reset") }
        val vm = stockVm(api)
        advanceUntilIdle()

        vm.add("5")
        advanceUntilIdle()
        vm.add("7")
        advanceUntilIdle()

        assertThat(api.keys[1]).isNotEqualTo(api.keys[0])
    }

    @Test
    fun `the key for an unanswered save survives process death`() = runTest(dispatcher) {
        val api = StockApi()
        api.answers += { _ -> throw IOException("reset") }
        val saved = SavedStateHandle(mapOf("variantId" to "v-1"))
        val first = stockVm(api, saved)
        advanceUntilIdle()
        first.add("5")
        advanceUntilIdle()

        val second = stockVm(api, saved)
        advanceUntilIdle()
        second.add("5")
        advanceUntilIdle()

        assertThat(api.keys[1]).isEqualTo(api.keys[0])
    }

    @Test
    fun `a definite refusal spends the key and a conflict starts afresh`() = runTest(dispatcher) {
        val api = StockApi()
        api.answers += { _ -> refused("STOCK_RESERVED") }
        api.answers += { _ ->
            Response.error(
                409,
                """{"error":{"code":"IDEMPOTENCY_CONFLICT","message":"m"}}"""
                    .toResponseBody("application/json".toMediaType()),
            )
        }
        val vm = stockVm(api)
        advanceUntilIdle()

        vm.add("5")
        advanceUntilIdle()
        vm.submit()
        advanceUntilIdle()
        val readsBefore = api.reads
        vm.submit()
        advanceUntilIdle()

        assertThat(api.keys).hasSize(3)
        assertThat(api.keys.toSet()).hasSize(3)
        // The conflict re-read the level; the success shows the server's level.
        assertThat(readsBefore).isAtLeast(2)
        assertThat(vm.content().level.total).isEqualTo(15)
    }

    // ─── M4 / M5 / addendum: documents ───────────────────────────────

    private class DocApi : FakeCommerceApi() {
        val saves = mutableListOf<SaveDocumentsRequest>()
        override suspend fun saveDocuments(body: SaveDocumentsRequest): Response<ApiEnvelope<Unit>> {
            saves += body
            return envelope(Unit)
        }
    }

    private fun pdf() = MediaSourceResolver { uri ->
        PickedMedia(uri = uri, mimeType = "application/pdf", sizeBytes = 2_000L, source = mockk(relaxed = true))
    }

    private fun uploader(
        reserve: AppResult<MediaInitDto> = AppResult.Success(MediaInitDto(mediaId = "m-1", uploadUrl = "https://u/p")),
        confirm: AppResult<MediaAssetDto> = AppResult.Success(mockk(relaxed = true)),
    ): MediaUploader = mockk<MediaUploader>().also { u ->
        coEvery { u.reserve(any(), any(), any(), any(), any()) } returns reserve
        coEvery { u.upload(any(), any(), any(), any(), any()) } returns PresignedPutResult.Success
        coEvery { u.confirm(any()) } returns confirm
    }

    @Test
    fun `a PAN PDF is reserved as a private document and attached`() = runTest(dispatcher) {
        val api = DocApi()
        val up = uploader()
        val vm = DocumentViewModel(CommerceRepository(api), up, pdf(), dispatcher)

        vm.upload("content://pan.pdf") {}
        advanceUntilIdle()

        // The wire value media-service binds, spelled out rather than via the constant.
        coVerify { up.reserve("application/pdf", 2_000L, any(), any(), "document") }
        assertThat(FILE_TYPE_DOCUMENT).isEqualTo("document")
        assertThat(api.saves.single().documents.single().mediaId).isEqualTo("m-1")
        assertThat(vm.state.value.error).isNull()
    }

    @Test
    fun `a document over the day's budget says when to come back`() = runTest(dispatcher) {
        val up = uploader(reserve = AppResult.Failure(AppError.RateLimited(retryAfterSeconds = 86_400)))
        val vm = DocumentViewModel(CommerceRepository(DocApi()), up, pdf(), dispatcher)

        vm.upload("content://pan.pdf") {}
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo(
            "You've sent as many documents as we accept in a day. Try again tomorrow.",
        )
    }

    @Test
    fun `bytes that are not what they claim are refused at confirm in words`() = runTest(dispatcher) {
        val up = uploader(
            confirm = AppResult.Failure(AppError.Unknown(code = "UPLOAD_INTEGRITY_ERROR", statusCode = 409)),
        )
        val api = DocApi()
        val vm = DocumentViewModel(CommerceRepository(api), up, pdf(), dispatcher)

        vm.upload("content://renamed.pdf") {}
        advanceUntilIdle()

        assertThat(vm.state.value.error).isEqualTo(DOCUMENT_NOT_WHAT_IT_SAYS)
        assertThat(api.saves).isEmpty()
    }

    @Test
    fun `each document refusal reads plainly`() {
        fun copy(error: AppError, step: UploadStep = UploadStep.Reserve) = documentUploadCopy(error, step, "fallback")

        assertThat(copy(AppError.Unknown(code = "UNSUPPORTED_MEDIA_TYPE", statusCode = 415)))
            .isEqualTo(DOCUMENT_WRONG_TYPE)
        assertThat(copy(AppError.Unknown(code = null, statusCode = 415))).isEqualTo(DOCUMENT_WRONG_TYPE)
        assertThat(copy(AppError.Unknown(code = "FILE_TOO_LARGE", statusCode = 413))).isEqualTo(DOCUMENT_TOO_LARGE)
        assertThat(copy(AppError.Unknown(code = "UPLOAD_STATE_CONFLICT", statusCode = 409), UploadStep.Confirm))
            .isEqualTo(DOCUMENT_PICK_AGAIN)
        // A 409 at RESERVE is not a confirm refusal.
        assertThat(copy(AppError.Unknown(code = "UPLOAD_INTEGRITY_ERROR", statusCode = 409))).isEqualTo("fallback")
        assertThat(copy(AppError.AuthFailed())).isEqualTo(UPLOAD_SIGNED_OUT)
        assertThat(copy(AppError.Timeout())).isEqualTo(UPLOAD_OFFLINE)
        // Today's magic-byte mismatch is a 500 (server follow-up): the fallback, not a guess.
        assertThat(copy(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"), UploadStep.Confirm))
            .isEqualTo("fallback")
    }

    @Test
    fun `a product photo over the hourly budget says how long to wait`() {
        assertThat(productImageUploadCopy(AppError.RateLimited(retryAfterSeconds = 1_500), "f"))
            .isEqualTo("You've uploaded a lot in the last hour. Try again in 25 minutes.")
        assertThat(productImageUploadCopy(AppError.Unknown(code = "FILE_TOO_LARGE", statusCode = 413), "f"))
            .isEqualTo("That photo is too large.")
        assertThat(productImageUploadCopy(AppError.Server(statusCode = 502, code = null), "f")).isEqualTo("f")
    }

    @Test
    fun `a product photo refused for the hourly budget says so on that photo`() = runTest(dispatcher) {
        val jpeg = MediaSourceResolver { uri ->
            PickedMedia(uri = uri, mimeType = "image/jpeg", sizeBytes = 1_000L, source = mockk(relaxed = true))
        }
        val up = uploader(reserve = AppResult.Failure(AppError.RateLimited(retryAfterSeconds = 600)))
        val vm = ProductImagesViewModel(CommerceRepository(FakeCommerceApi()), up, jpeg, SavedStateHandle(), dispatcher)

        vm.onPicked(listOf("content://photo-1"))
        advanceUntilIdle()

        assertThat(vm.state.value.images.single().error)
            .isEqualTo("You've uploaded a lot in the last hour. Try again in 10 minutes.")
    }

    @Test
    fun `the wait reads in words a person would say`() {
        assertThat(waitFor(null)).isEqualTo("later")
        assertThat(waitFor(0)).isEqualTo("later")
        assertThat(waitFor(30)).isEqualTo("in a minute")
        assertThat(waitFor(61)).isEqualTo("in 2 minutes")
        assertThat(waitFor(3_600)).isEqualTo("in an hour")
        assertThat(waitFor(7_000)).isEqualTo("in 2 hours")
        assertThat(waitFor(86_400)).isEqualTo("tomorrow")
    }

    // ─── L5: payout prefill ──────────────────────────────────────────

    private class ProfileApi(private val gate: CompletableDeferred<Unit>) : FakeCommerceApi() {
        override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> {
            gate.await()
            return envelope(SellerProfileDto(id = "s-1", storeName = "Asha Books"))
        }
    }

    @Test
    fun `the prefill never overwrites a name the seller typed`() = runTest(dispatcher) {
        val gate = CompletableDeferred<Unit>()
        val vm = PayoutViewModel(CommerceRepository(ProfileApi(gate)))
        advanceUntilIdle()

        vm.update { it.copy(accountHolderName = "Asha Rao") }
        gate.complete(Unit)
        advanceUntilIdle()

        assertThat(vm.form.value.accountHolderName).isEqualTo("Asha Rao")
    }

    @Test
    fun `the prefill fills a blank name`() = runTest(dispatcher) {
        val gate = CompletableDeferred(Unit)
        val vm = PayoutViewModel(CommerceRepository(ProfileApi(gate)))
        advanceUntilIdle()

        assertThat(vm.form.value.accountHolderName).isEqualTo("Asha Books")
    }

    // ─── L6: prices ──────────────────────────────────────────────────

    @Test
    fun `a pasted long number is not a price and can never wrap round`() {
        assertThat(parseRupees("999999999")).isEqualTo(Paise(99_999_999_900))
        assertThat(parseRupees("999999999.99")).isEqualTo(Paise(99_999_999_999))
        assertThat(parseRupees("1234567890")).isNull()
        // 92233720368547759 × 100 wraps a Long; 184467440737095517 wraps to a small positive.
        assertThat(parseRupees("92233720368547759")).isNull()
        assertThat(parseRupees("184467440737095517")).isNull()
        assertThat(parseRupees("9223372036854775807")).isNull()
        // Leading zeros are not digits of the price.
        assertThat(parseRupees("0000000001299")).isEqualTo(Paise(129_900))
    }

    // ─── Business page onboarding ────────────────────────────────────

    @Test
    fun `the business page refusals read plainly`() {
        assertThat(
            CommerceError.Unexpected("BUSINESS_PAGE_NOT_FOUND", "business page not found", httpStatus = 404)
                .describeForSeller(),
        ).isEqualTo(BUSINESS_PAGE_NOT_FOUND_COPY)
        assertThat(
            CommerceError.Unexpected("BUSINESS_PAGE_CHECK_UNAVAILABLE", "m", httpStatus = 503).describeForSeller(),
        ).isEqualTo(BUSINESS_PAGE_CHECK_UNAVAILABLE_COPY)
    }

    @Test
    fun `an unavailable business page check leaves the form open to try again`() = runTest(dispatcher) {
        var calls = 0
        val api = object : FakeCommerceApi() {
            override suspend fun startSelling(body: StartSellingRequest): Response<ApiEnvelope<SellerProfileDto>> {
                calls++
                return if (calls == 1) {
                    Response.error(
                        503,
                        """{"error":{"code":"BUSINESS_PAGE_CHECK_UNAVAILABLE","message":"m"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    )
                } else {
                    envelope(SellerProfileDto(id = "s-1", storeName = body.storeName))
                }
            }
        }
        val vm = StartSellingViewModel(CommerceRepository(api))
        vm.update { it.copy(storeName = "Asha Books", email = "asha@example.com") }
        var opened = 0

        vm.submit { opened++ }
        advanceUntilIdle()
        assertThat(vm.form.value.error).isEqualTo(BUSINESS_PAGE_CHECK_UNAVAILABLE_COPY)
        assertThat(vm.form.value.saving).isFalse()

        vm.submit { opened++ }
        advanceUntilIdle()
        assertThat(opened).isEqualTo(1)
    }
}
