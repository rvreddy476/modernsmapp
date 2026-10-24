package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.NewProduct
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.UpdateVariantRequest
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.SUBMITTED_FOR_REVIEW
import com.us.android.feature.commerce.seller.SellerUiState
import com.us.android.feature.commerce.seller.SellerViewModel
import com.us.android.feature.commerce.seller.describeForSeller
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response
import java.io.File

/**
 * Sending a listing for review from the hub, through the REAL repository and
 * ViewModel. The submit used to run in a ViewModel nothing collected, called
 * with an empty callback: a refusal looked exactly like a success.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SellerSubmitViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class HubApi : FakeCommerceApi() {
        var approval = "changes_requested"
        var productsFail = false
        var submitAnswer: Response<ApiEnvelope<Unit>> = Response.success(204, ApiEnvelope(data = null))
        var gate: CompletableDeferred<Unit>? = null
        val submits = mutableListOf<String>()

        override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> =
            envelope(SellerProfileDto(id = "s-1", storeName = "Shop", status = "approved"))

        override suspend fun sellerProducts(
            status: String?,
            limit: Int,
            offset: Int,
        ): Response<ApiEnvelope<SellerProductsDto>> {
            if (productsFail && submits.isNotEmpty()) return unused()
            return envelope(
                SellerProductsDto(
                    items = listOf(
                        SellerProductDto(
                            id = "p-1",
                            title = "Diya",
                            status = "draft",
                            approvalStatus = approval,
                            rejectionReason = "Add a photo of all four",
                        ),
                    ),
                ),
            )
        }

        override suspend fun submitProduct(productId: String): Response<ApiEnvelope<Unit>> {
            submits += productId
            gate?.await()
            return submitAnswer
        }
    }

    private fun SellerViewModel.content() = state.value as SellerUiState.Content

    private fun error(status: Int, body: String): Response<ApiEnvelope<Unit>> =
        Response.error(status, body.toResponseBody("application/json".toMediaType()))

    @Test
    fun `a resubmit that succeeds re-reads the catalogue and says so`() = runTest(dispatcher) {
        val api = HubApi()
        val vm = SellerViewModel(CommerceRepository(api))
        advanceUntilIdle()

        api.approval = "submitted" // what the server now holds
        vm.submitProduct("p-1")
        advanceUntilIdle()

        val after = vm.content()
        assertThat(api.submits).containsExactly("p-1")
        assertThat(after.submittingProductId).isNull()
        assertThat(after.submitProblem).isNull()
        assertThat(after.message).isEqualTo(SUBMITTED_FOR_REVIEW)
        assertThat(after.products.single().approvalStatus).isEqualTo("submitted")
    }

    @Test
    fun `the row shows progress while the submit is in flight, and a second tap is ignored`() =
        runTest(dispatcher) {
            val api = HubApi().apply { gate = CompletableDeferred() }
            val vm = SellerViewModel(CommerceRepository(api))
            advanceUntilIdle()

            vm.submitProduct("p-1")
            advanceUntilIdle()
            assertThat(vm.content().submittingProductId).isEqualTo("p-1")
            vm.submitProduct("p-1")
            advanceUntilIdle()

            api.gate!!.complete(Unit)
            advanceUntilIdle()
            assertThat(api.submits).hasSize(1)
            assertThat(vm.content().submittingProductId).isNull()
        }

    @Test
    fun `an incomplete listing's refusal lands under its row with every gap`() = runTest(dispatcher) {
        val api = HubApi().apply {
            submitAnswer = error(
                422,
                """{"error":{"code":"PRODUCT_INCOMPLETE","message":"this listing is not ready for review yet",""" +
                    """"details":{"fields":[""" +
                    """{"code":"listing.image","label":"Product image","reason":"no image"},""" +
                    """{"code":"hsn_code","label":"HSN Code","reason":"required"}]}}}""",
            )
        }
        val vm = SellerViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.submitProduct("p-1")
        advanceUntilIdle()

        val problem = checkNotNull(vm.content().submitProblem)
        assertThat(problem.productId).isEqualTo("p-1")
        assertThat(problem.message).isEqualTo("This listing isn't ready for review yet.")
        assertThat(problem.gaps).containsExactly("Add at least one product photo.", "HSN Code is required.").inOrder()
        assertThat(vm.content().submittingProductId).isNull()
        assertThat(vm.content().message).isNull()

        vm.dismissSubmitProblem()
        assertThat(vm.content().submitProblem).isNull()
    }

    @Test
    fun `a refusal by state is said in words`() = runTest(dispatcher) {
        val api = HubApi().apply {
            submitAnswer = error(409, """{"error":{"code":"PRODUCT_NOT_SUBMITTABLE","message":"x"}}""")
        }
        val vm = SellerViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.submitProduct("p-1")
        advanceUntilIdle()

        val problem = checkNotNull(vm.content().submitProblem)
        assertThat(problem.message).startsWith("This listing can't be submitted now.")
        assertThat(problem.gaps).isEmpty()
    }

    @Test
    fun `a submit that went through is shown even when the re-read fails`() = runTest(dispatcher) {
        val api = HubApi().apply { productsFail = true }
        val vm = SellerViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.submitProduct("p-1")
        advanceUntilIdle()

        // No second "Resubmit" offered for a listing now in the queue.
        assertThat(vm.content().products.single().approvalStatus).isEqualTo("submitted")
        assertThat(vm.content().message).isEqualTo(SUBMITTED_FOR_REVIEW)
    }

    // ─── The seller refusal goldens, as the seller reads them ────────

    private fun golden(name: String) = File("../../core/commerce/src/test/resources/contracts/seller/$name").readText()

    private class RefusingApi(private val status: Int, private val raw: String) : FakeCommerceApi() {
        private fun <T> refusal(): Response<ApiEnvelope<T>> =
            Response.error(status, raw.toResponseBody("application/json".toMediaType()))

        override suspend fun updateVariant(variantId: String, body: UpdateVariantRequest) =
            refusal<VariantDto>()

        override suspend fun createProduct(body: CreateProductRequest) = refusal<SellerProductDto>()
    }

    private fun failureCopy(result: CommerceResult<*>): String =
        (result as CommerceResult.Failure).error.describeForSeller()

    @Test
    fun `every refusal golden reads as its sentence`() = runTest(dispatcher) {
        val priceAboveMrp = CommerceRepository(RefusingApi(400, golden("variant_patch_400_price_above_mrp.json")))
            .updateVariant("v-1", sellingPrice = Paise(1))
        val skuLocked = CommerceRepository(RefusingApi(409, golden("variant_patch_409_sku_locked.json")))
            .updateVariant("v-1", sku = "NEW")
        val returnPolicy = CommerceRepository(RefusingApi(400, golden("product_patch_400_return_policy_invalid.json")))
            .createProduct(
                NewProduct(
                    title = "T",
                    description = null,
                    taxClassId = "t",
                    sku = "S",
                    mrp = Paise(2),
                    sellingPrice = Paise(1),
                    openingStock = 1,
                ),
            )

        assertThat(failureCopy(priceAboveMrp)).isEqualTo("Selling price can't be higher than the MRP.")
        assertThat(failureCopy(skuLocked)).isEqualTo("This SKU can't change once the listing is live.")
        assertThat(failureCopy(returnPolicy))
            .isEqualTo("Choose a return window: no returns, 7, 15 or 30 days, or custom.")
    }
}
