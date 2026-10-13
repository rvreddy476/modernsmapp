package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.CategoryChoice
import com.us.android.core.commerce.network.CategoryDto
import com.us.android.core.commerce.network.CategoryTreeDto
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.TaxClassDto
import com.us.android.core.commerce.network.TaxClassListDto
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.commerce.network.VariantListDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.CHOOSE_A_CATEGORY
import com.us.android.feature.commerce.seller.CreatedProduct
import com.us.android.feature.commerce.seller.NewProductForm
import com.us.android.feature.commerce.seller.NewProductViewModel
import com.us.android.feature.commerce.seller.createRefusal
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
 * Listing a product needs a category (2026-09-30), and hands the caller the
 * new listing's VARIANT id.
 *
 * A product created without `category_id` sits in no category, so a buyer
 * browsing never reaches it. The create is refused on the device with one
 * line, "Choose a category", rather than by a button that never enables —
 * with five fields on the form a seller cannot tell which one is holding it.
 * The category comes from the tree's listable leaves. Through the REAL
 * repository and ViewModel over the fake wire.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class NewProductCategoryTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class ListingApi(private val treeMissing: Boolean = false) : FakeCommerceApi() {
        val creates = mutableListOf<CreateProductRequest>()

        override suspend fun taxClasses(): Response<ApiEnvelope<TaxClassListDto>> =
            envelope(TaxClassListDto(listOf(TaxClassDto(id = "tax-18", name = "GST 18%", ratePercent = 18.0))))

        override suspend fun categoryTree(): Response<ApiEnvelope<List<CategoryTreeDto>>> =
            if (treeMissing) {
                // An older server answers the FLAT shape to the same URL.
                envelope(listOf(CategoryTreeDto(id = "kitchen", name = "Kitchen")))
            } else {
                envelope(
                    listOf(
                        CategoryTreeDto(
                            id = "home",
                            name = "Home",
                            isListable = false,
                            children = listOf(CategoryTreeDto(id = "kitchen", name = "Kitchen", isListable = true)),
                        ),
                    ),
                )
            }

        override suspend fun categories(): Response<ApiEnvelope<List<CategoryDto>>> =
            envelope(listOf(CategoryDto(id = "kitchen", name = "Kitchen"), CategoryDto(id = "books", name = "Books")))

        override suspend fun createProduct(body: CreateProductRequest): Response<ApiEnvelope<SellerProductDto>> {
            creates += body
            return envelope(
                SellerProductDto(id = "p-9", title = body.title, status = "draft", approvalStatus = "draft"),
            )
        }

        override suspend fun productVariants(productId: String): Response<ApiEnvelope<VariantListDto>> =
            envelope(VariantListDto(listOf(VariantDto(id = "v-9", sku = "SKU-9"))))
    }

    private fun filled(vm: NewProductViewModel, categoryId: String? = null) {
        vm.update {
            it.copy(
                title = "Steel kettle",
                sellingPrice = "1299",
                openingStock = "5",
                taxClassId = "tax-18",
                categoryId = categoryId,
            )
        }
    }

    @Test
    fun `the picker offers the tree's listable leaves, by path`() = runTest(dispatcher) {
        val vm = NewProductViewModel(CommerceRepository(ListingApi()))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(vm.form.value.loadingCategories).isFalse()
        assertThat(vm.form.value.categories).containsExactly(CategoryChoice("kitchen", "Home › Kitchen"))
        // Never preselected.
        assertThat(vm.form.value.categoryId).isNull()
    }

    @Test
    fun `an older server without the tree falls back to the flat list's leaves`() = runTest(dispatcher) {
        val vm = NewProductViewModel(CommerceRepository(ListingApi(treeMissing = true)))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(vm.form.value.categories.map { it.id }).containsExactly("kitchen", "books").inOrder()
    }

    @Test
    fun `a create without a category is refused with one line and nothing is sent`() = runTest(dispatcher) {
        val api = ListingApi()
        val vm = NewProductViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()
        filled(vm, categoryId = null)
        var created: CreatedProduct? = null

        // The button is live: the form is complete by every other measure.
        assertThat(vm.form.value.isComplete).isTrue()
        vm.submit { created = it }
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.creates).isEmpty()
        assertThat(created).isNull()
        assertThat(vm.form.value.error).isEqualTo(CHOOSE_A_CATEGORY)
        assertThat(vm.form.value.saving).isFalse()
    }

    @Test
    fun `a create with a category sends category_id and hands back the variant id`() = runTest(dispatcher) {
        val api = ListingApi()
        val vm = NewProductViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()
        filled(vm, categoryId = "kitchen")
        var created: CreatedProduct? = null

        vm.submit { created = it }
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.creates.single().categoryId).isEqualTo("kitchen")
        assertThat(api.creates.single().taxClassId).isEqualTo("tax-18")
        assertThat(created).isEqualTo(CreatedProduct(productId = "p-9", title = "Steel kettle", variantId = "v-9"))
        // Never the product id.
        assertThat(created?.variantId).isNotEqualTo("p-9")
    }

    // ─── The pure rule ───────────────────────────────────────────────

    @Test
    fun `the refusal is exactly a missing category`() {
        assertThat(createRefusal(NewProductForm(categoryId = null))).isEqualTo(CHOOSE_A_CATEGORY)
        assertThat(createRefusal(NewProductForm(categoryId = ""))).isEqualTo(CHOOSE_A_CATEGORY)
        assertThat(createRefusal(NewProductForm(categoryId = "kitchen"))).isNull()
    }

    @Test
    fun `the chosen category is labelled by its path`() {
        val form = NewProductForm(
            categoryId = "kitchen",
            categories = listOf(CategoryChoice("kitchen", "Home › Kitchen")),
        )
        assertThat(form.categoryLabel).isEqualTo("Home › Kitchen")
        assertThat(form.copy(categoryId = null).categoryLabel).isNull()
    }
}
