package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.commerce.network.VariantListDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.NO_VARIANT_TO_EDIT
import com.us.android.feature.commerce.seller.SellerUiState
import com.us.android.feature.commerce.seller.SellerViewModel
import com.us.android.feature.commerce.seller.VariantTarget
import com.us.android.feature.commerce.seller.variantTarget
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * What opens when a seller taps a product row: the editor for its VARIANT.
 *
 * The 2026-09-30 fix. The row used to open the stock screen with the
 * PRODUCT id as the variant id, on the comment "the P0 catalogue is
 * single-variant"; the server looks up `product_variants.id`, so every stock
 * and price edit answered variant-not-found. Through the REAL repository and
 * ViewModel over the fake wire: the id handed to the navigation callback is
 * one the server named as a variant, and a product id never is.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SellerVariantResolutionTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class HubApi(
        private val rows: List<SellerProductDto>,
        private val variantsByProduct: Map<String, List<VariantDto>> = emptyMap(),
    ) : FakeCommerceApi() {
        val variantReads = mutableListOf<String>()

        override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> =
            envelope(SellerProfileDto(id = "s-1", storeName = "Shop", status = "approved"))

        override suspend fun sellerProducts(
            status: String?,
            limit: Int,
            offset: Int,
        ): Response<ApiEnvelope<SellerProductsDto>> = envelope(SellerProductsDto(items = rows, total = rows.size))

        override suspend fun productVariants(productId: String): Response<ApiEnvelope<VariantListDto>> {
            variantReads += productId
            return envelope(VariantListDto(items = variantsByProduct[productId].orEmpty()))
        }
    }

    private fun row(id: String, variants: List<VariantDto> = emptyList()) =
        SellerProductDto(
            id = id,
            title = "Kettle $id",
            status = "active",
            approvalStatus = "approved",
            variants = variants,
        )

    private fun content(vm: SellerViewModel) = vm.state.value as SellerUiState.Content

    @Test
    fun `a bare row reads the product's variants and opens the single one, never the product id`() =
        runTest(dispatcher) {
            val api = HubApi(
                rows = listOf(row("p-1")),
                variantsByProduct = mapOf("p-1" to listOf(VariantDto(id = "v-1"))),
            )
            val vm = SellerViewModel(CommerceRepository(api))
            dispatcher.scheduler.advanceUntilIdle()
            val opened = mutableListOf<Pair<String, String>>()

            vm.openStock(content(vm).products.single()) { variantId, title -> opened += variantId to title }
            dispatcher.scheduler.advanceUntilIdle()

            assertThat(api.variantReads).containsExactly("p-1")
            assertThat(opened).containsExactly("v-1" to "Kettle p-1")
            assertThat(opened.map { it.first }).doesNotContain("p-1")
            assertThat(content(vm).chooser).isNull()
            assertThat(content(vm).resolvingProductId).isNull()
        }

    @Test
    fun `a row that carries its variant opens it without a second read`() = runTest(dispatcher) {
        val api = HubApi(rows = listOf(row("p-1", variants = listOf(VariantDto(id = "v-1", sku = "SKU-1")))))
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()
        val opened = mutableListOf<String>()

        vm.openStock(content(vm).products.single()) { variantId, _ -> opened += variantId }
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.variantReads).isEmpty()
        assertThat(opened).containsExactly("v-1")
    }

    @Test
    fun `three variants open a chooser, and the chosen one is what opens`() = runTest(dispatcher) {
        val three = listOf(
            VariantDto(id = "v-s", option1Name = "Size", option1Value = "S"),
            VariantDto(id = "v-m", option1Name = "Size", option1Value = "M"),
            VariantDto(id = "v-l", option1Name = "Size", option1Value = "L"),
        )
        val api = HubApi(rows = listOf(row("p-1")), variantsByProduct = mapOf("p-1" to three))
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()
        val opened = mutableListOf<String>()

        vm.openStock(content(vm).products.single()) { variantId, _ -> opened += variantId }
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(opened).isEmpty()
        val chooser = checkNotNull(content(vm).chooser)
        assertThat(chooser.variants.map { it.id }).containsExactly("v-s", "v-m", "v-l").inOrder()

        vm.chooseVariant(chooser.variants[1]) { variantId, _ -> opened += variantId }

        assertThat(opened).containsExactly("v-m")
        assertThat(content(vm).chooser).isNull()
    }

    @Test
    fun `no variant anywhere is said as one line, and nothing opens`() = runTest(dispatcher) {
        val api = HubApi(rows = listOf(row("p-1")))
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()
        val opened = mutableListOf<String>()

        vm.openStock(content(vm).products.single()) { variantId, _ -> opened += variantId }
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(opened).isEmpty()
        assertThat(content(vm).message).isEqualTo(NO_VARIANT_TO_EDIT)
    }

    @Test
    fun `dismissing the chooser opens nothing`() = runTest(dispatcher) {
        val two = listOf(VariantDto(id = "v-1"), VariantDto(id = "v-2"))
        val api = HubApi(rows = listOf(row("p-1")), variantsByProduct = mapOf("p-1" to two))
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.openStock(content(vm).products.single()) { _, _ -> error("must not open") }
        dispatcher.scheduler.advanceUntilIdle()
        vm.dismissChooser()

        assertThat(content(vm).chooser).isNull()
    }

    // ─── The pure rule ───────────────────────────────────────────────

    private fun variant(id: String) = Variant(id, "", emptyList(), Paise.ZERO, Paise.ZERO, true, 1)

    @Test
    fun `the target is one, several or none by count`() {
        assertThat(variantTarget(emptyList())).isEqualTo(VariantTarget.None)
        assertThat(variantTarget(listOf(variant("v-1")))).isEqualTo(VariantTarget.One("v-1"))
        val two = listOf(variant("v-1"), variant("v-2"))
        assertThat(variantTarget(two)).isEqualTo(VariantTarget.Several(two))
    }
}
