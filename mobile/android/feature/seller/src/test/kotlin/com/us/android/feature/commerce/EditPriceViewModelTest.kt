package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerVariantDto
import com.us.android.core.commerce.network.UpdateVariantRequest
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.feature.commerce.seller.EditPriceUiState
import com.us.android.feature.commerce.seller.EditPriceViewModel
import com.us.android.feature.commerce.seller.afterRefusal
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
 * The variant editor through the REAL repository and ViewModel, against the
 * commerce-service goldens (f6dce2470) for what a variant PATCH now answers.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class EditPriceViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class VariantApi(
        var variantStatus: String = "active",
        var approvalStatus: String? = "approved",
    ) : FakeCommerceApi() {
        val patches = mutableListOf<UpdateVariantRequest>()
        var answer: (() -> Response<ApiEnvelope<VariantDto>>)? = null

        override suspend fun sellerVariant(variantId: String): Response<ApiEnvelope<SellerVariantDto>> =
            envelope(
                SellerVariantDto(
                    variantId = variantId,
                    productId = "p-1",
                    title = "Earbuds",
                    sku = "MOM-EB-WHT",
                    sellingPriceMinor = Paise(129_900),
                    mrpMinor = Paise(149_900),
                    status = variantStatus,
                ),
            )

        override suspend fun sellerProducts(
            status: String?,
            limit: Int,
            offset: Int,
        ): Response<ApiEnvelope<SellerProductsDto>> {
            val approval = approvalStatus ?: return unused()
            return envelope(
                SellerProductsDto(
                    items = listOf(
                        SellerProductDto(id = "p-0", title = "Other", status = "active", approvalStatus = "draft"),
                        SellerProductDto(id = "p-1", title = "Earbuds", status = "active", approvalStatus = approval),
                    ),
                ),
            )
        }

        override suspend fun updateVariant(
            variantId: String,
            body: UpdateVariantRequest,
        ): Response<ApiEnvelope<VariantDto>> {
            patches += body
            return answer?.invoke() ?: envelope(VariantDto(id = variantId))
        }
    }

    private fun viewModel(api: VariantApi) =
        EditPriceViewModel(CommerceRepository(api), SavedStateHandle(mapOf("variantId" to "v-1")))

    private fun EditPriceViewModel.content() = state.value as EditPriceUiState.Content

    // ─── The switch ──────────────────────────────────────────────────

    @Test
    fun `pausing sends inactive and resuming sends active`() = runTest(dispatcher) {
        val api = VariantApi()
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.setPaused(true)
        advanceUntilIdle()
        assertThat(vm.content().paused).isTrue()
        vm.setPaused(false)
        advanceUntilIdle()

        assertThat(api.patches.map { it.status }).containsExactly("inactive", "active").inOrder()
        // A switch flip sends the status alone.
        assertThat(api.patches.map { it.sellingPriceMinor to it.sku }).containsExactly(null to null, null to null)
        assertThat(vm.content().paused).isFalse()
    }

    @Test
    fun `an inactive variant loads as paused, an out-of-stock one does not`() = runTest(dispatcher) {
        val inactive = viewModel(VariantApi(variantStatus = "inactive"))
        val soldOut = viewModel(VariantApi(variantStatus = "out_of_stock"))
        advanceUntilIdle()

        assertThat(inactive.content().paused).isTrue()
        assertThat(soldOut.content().paused).isFalse()
        assertThat(soldOut.content().outOfStock).isTrue()
    }

    // ─── The SKU ─────────────────────────────────────────────────────

    @Test
    fun `an approved listing's SKU is read-only and never sent`() = runTest(dispatcher) {
        val api = VariantApi(approvalStatus = "approved")
        val vm = viewModel(api)
        advanceUntilIdle()
        assertThat(vm.content().skuEditable).isFalse()
        assertThat(vm.content().currentSku).isEqualTo("MOM-EB-WHT")

        vm.setSku("MOM-EB-NEW")
        vm.setPrice("1199")
        vm.save {}
        advanceUntilIdle()

        assertThat(api.patches.single().sku).isNull()
        assertThat(api.patches.single().sellingPriceMinor).isEqualTo(Paise(119_900))
    }

    @Test
    fun `a draft listing's SKU can change, and only a changed one is sent`() = runTest(dispatcher) {
        val api = VariantApi(approvalStatus = "changes_requested")
        val vm = viewModel(api)
        advanceUntilIdle()
        assertThat(vm.content().skuEditable).isTrue()

        vm.setSku("MOM-EB-WHT") // the SKU it already has
        assertThat(vm.content().canSave).isFalse()

        vm.setSku("  MOM-EB-NEW  ")
        vm.save {}
        advanceUntilIdle()

        assertThat(api.patches.single().sku).isEqualTo("MOM-EB-NEW")
        assertThat(api.patches.single().sellingPriceMinor).isNull()
    }

    @Test
    fun `a catalogue that cannot be read leaves the SKU locked`() = runTest(dispatcher) {
        val vm = viewModel(VariantApi(approvalStatus = null))
        advanceUntilIdle()

        assertThat(vm.content().skuEditable).isFalse()
        // The price is still editable: the screen did not fail.
        assertThat(vm.content().currentPrice).isEqualTo(Paise(129_900))
    }

    // ─── What a save answers (the goldens) ───────────────────────────

    private val json = NetworkModule.provideJson()

    private fun golden(name: String): Pair<Int, String> {
        val raw = File("../../core/commerce/src/test/resources/contracts/seller/$name").readText()
        val status = Regex("""_(\d{3})_""").find(name)!!.groupValues[1].toInt()
        return status to raw
    }

    private fun answerWith(api: VariantApi, name: String) {
        val (status, raw) = golden(name)
        api.answer = {
            if (status in 200..299) {
                // The golden decoded as the wire would be: the new row, no
                // `revalidated`, no notice.
                Response.success(json.decodeFromString(ApiEnvelope.serializer(VariantDto.serializer()), raw))
            } else {
                Response.error(status, raw.toResponseBody("application/json".toMediaType()))
            }
        }
    }

    @Test
    fun `a price change on a live listing saves and goes live, with no review notice`() = runTest(dispatcher) {
        val api = VariantApi(approvalStatus = "approved")
        answerWith(api, "variant_patch_200_price_live.json")
        val vm = viewModel(api)
        advanceUntilIdle()
        var saved = false

        vm.setPrice("1199")
        vm.save { saved = true }
        advanceUntilIdle()

        assertThat(saved).isTrue()
        val after = vm.content()
        assertThat(after.error).isNull()
        assertThat(after.saving).isFalse()
    }

    @Test
    fun `a price above the MRP is refused in plain words`() = runTest(dispatcher) {
        val api = VariantApi()
        answerWith(api, "variant_patch_400_price_above_mrp.json")
        val vm = viewModel(api)
        advanceUntilIdle()
        var saved = false

        vm.setPrice("1399") // below the MRP the screen knows; the server judged otherwise
        vm.save { saved = true }
        advanceUntilIdle()

        assertThat(saved).isFalse()
        assertThat(vm.content().error).isEqualTo("Selling price can't be higher than the MRP.")
        // The seller's typing is kept for the next try.
        assertThat(vm.content().price).isEqualTo("1399")
    }

    @Test
    fun `a SKU refused as locked locks the field and keeps the price`() = runTest(dispatcher) {
        // The listing was approved since the screen loaded.
        val api = VariantApi(approvalStatus = "draft")
        answerWith(api, "variant_patch_409_sku_locked.json")
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.setSku("MOM-EB-NEW")
        vm.setPrice("1199")
        vm.save {}
        advanceUntilIdle()

        val after = vm.content()
        assertThat(after.error).isEqualTo("This SKU can't change once the listing is live.")
        assertThat(after.skuEditable).isFalse()
        assertThat(after.sku).isEmpty()
        assertThat(after.newSku).isNull()
        assertThat(after.price).isEqualTo("1199")
        assertThat(after.canSave).isTrue()
    }

    @Test
    fun `any other refusal leaves the SKU field as it was`() {
        val form = EditPriceUiState.Content(
            currentPrice = Paise(129_900),
            currentMrp = Paise(149_900),
            paused = false,
            currentSku = "A",
            skuEditable = true,
            sku = "B",
            saving = true,
        )
        val after = afterRefusal(form, CommerceError.Unexpected("SKU_TAKEN", "", httpStatus = 409))

        assertThat(after.skuEditable).isTrue()
        assertThat(after.sku).isEqualTo("B")
        assertThat(after.saving).isFalse()
        assertThat(after.error).isEqualTo("That SKU is already in use. Each SKU has to be unique.")
    }
}
