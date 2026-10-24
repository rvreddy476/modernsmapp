package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.SellScope
import com.us.android.core.commerce.model.SellScopeField
import com.us.android.core.commerce.model.SellScopeSaved
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.core.commerce.network.CategoryTreeDto
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.network.SaveSellScopeRequest
import com.us.android.core.commerce.network.SellScopeSavedDto
import com.us.android.core.commerce.network.SellScopeViewDto
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.TaxClassDto
import com.us.android.core.commerce.network.TaxClassListDto
import com.us.android.core.commerce.network.UpgradeSellScopeRequest
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.MediaAssetDto
import com.us.android.core.media.upload.MediaInitDto
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.BOOKS_ONLY_EDITOR_NOTE
import com.us.android.feature.commerce.seller.BOOKS_ONLY_LISTING_COPY
import com.us.android.feature.commerce.seller.DocumentViewModel
import com.us.android.feature.commerce.seller.NewProductViewModel
import com.us.android.feature.commerce.seller.SellScopeViewModel
import com.us.android.feature.commerce.seller.SellerUiState
import com.us.android.feature.commerce.seller.SellerViewModel
import com.us.android.feature.commerce.seller.SubmitShortcut
import com.us.android.feature.commerce.seller.UPGRADE_DONE_COPY
import com.us.android.feature.commerce.seller.describeForSeller
import com.us.android.feature.commerce.seller.documentChoices
import com.us.android.feature.commerce.seller.documentGuidance
import com.us.android.feature.commerce.seller.label
import com.us.android.feature.commerce.seller.offersUpgrade
import com.us.android.feature.commerce.seller.sellScopeRefusal
import com.us.android.feature.commerce.seller.sellScopeSavedLine
import com.us.android.feature.commerce.seller.submitShortcuts
import io.mockk.coEvery
import io.mockk.mockk
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.test.StandardTestDispatcher
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
 * Books-only sellers in MSeller (commerce-service migration 045, founder
 * 24 Oct 2026): the "What will you sell?" step, the checklist per scope, the
 * proof of address with both sides, the listing editor held to books at
 * GST 0%, and "Sell more categories". Through the REAL repository and
 * ViewModels over the fake wire, answered with the commerce goldens.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class BooksOnlySellerTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private fun golden(name: String) = File("../../core/commerce/src/test/resources/contracts/seller/$name").readText()

    /** The golden answered at the status in its name. */
    private fun <T> refusal(name: String): Response<ApiEnvelope<T>> {
        val status = Regex("""_(\d{3})_""").find(name)!!.groupValues[1].toInt()
        return Response.error(status, golden(name).toResponseBody("application/json".toMediaType()))
    }

    private suspend fun refusalOf(name: String) = (
        CommerceRepository(
            object : FakeCommerceApi() {
                override suspend fun saveSellScope(body: SaveSellScopeRequest): Response<ApiEnvelope<SellScopeSavedDto>> =
                    refusal(name)
            },
        ).saveSellScope(com.us.android.core.commerce.model.SellScopeChoice(SellScope.BOOKS_ONLY)) as CommerceResult.Failure
        ).error

    // ─── The sentences ───────────────────────────────────────────────

    @Test
    fun `every sell-scope refusal lands under its field, in the seller's words`() = runBlocking {
        val gstin = sellScopeRefusal(refusalOf("onboarding_sell_scope_put_400_gstin_required.json"))
        assertThat(gstin.field).isEqualTo(SellScopeField.GSTIN)
        assertThat(gstin.message).isEqualTo("Enter your GSTIN.")

        assertThat(sellScopeRefusal(refusalOf("onboarding_sell_scope_put_400_pan_invalid.json")).field)
            .isEqualTo(SellScopeField.PAN)

        val locked = sellScopeRefusal(refusalOf("onboarding_sell_scope_put_409_locked.json"))
        assertThat(locked.field).isNull()
        assertThat(locked.message).contains("with the reviewers")

        val mismatch = sellScopeRefusal(refusalOf("seller_sell_scope_upgrade_post_400_gstin_pan_mismatch.json"))
        assertThat(mismatch.field).isEqualTo(SellScopeField.GSTIN)
        assertThat(mismatch.message).contains("registered to a different PAN")

        assertThat(sellScopeRefusal(refusalOf("seller_sell_scope_upgrade_post_409_not_books_only.json")).message)
            .isEqualTo("Your shop already sells in every category.")
    }

    @Test
    fun `a listing outside books is refused in one sentence`() = runBlocking {
        val repo = CommerceRepository(
            object : FakeCommerceApi() {
                override suspend fun createProduct(body: CreateProductRequest): Response<ApiEnvelope<SellerProductDto>> =
                    refusal("product_post_403_seller_scope_books_only.json")
            },
        )
        val error = (repo.createProduct(newProduct()) as CommerceResult.Failure).error

        assertThat(error.describeForSeller()).isEqualTo(BOOKS_ONLY_LISTING_COPY)
    }

    @Test
    fun `the saved PAN reads masked, with its holder type`() {
        val saved = SellScopeSaved(SellScope.BOOKS_ONLY, null, "Asha Books", "XXXXXX234F", "INDIVIDUAL")
        assertThat(sellScopeSavedLine(saved)).isEqualTo("PAN XXXXXX234F · Individual")
        assertThat(sellScopeSavedLine(saved.copy(panMasked = null))).isNull()
    }

    // ─── The checklist ───────────────────────────────────────────────

    @Test
    fun `each gap offers its shortcut once, and a proof of address stands for the identity document`() {
        assertThat(
            submitShortcuts(
                listOf(
                    SellerRequirement.Pan,
                    SellerRequirement.BusinessName,
                    SellerRequirement.BusinessAddress,
                    SellerRequirement.AddressProof,
                    SellerRequirement.PickupAddress,
                    SellerRequirement.PayoutAccount,
                    SellerRequirement.KycDocument,
                ),
            ),
        ).containsExactly(
            SubmitShortcut.SELL_SCOPE,
            SubmitShortcut.BUSINESS_ADDRESS,
            SubmitShortcut.ADDRESS_PROOF,
            SubmitShortcut.PICKUP_ADDRESS,
            SubmitShortcut.PAYOUT,
        ).inOrder()

        assertThat(submitShortcuts(listOf(SellerRequirement.Gstin, SellerRequirement.KycDocument)))
            .containsExactly(SubmitShortcut.SELL_SCOPE, SubmitShortcut.DOCUMENT).inOrder()
    }

    @Test
    fun `the new readiness codes read as requirements, not as raw keys`() {
        for (key in listOf("sell_scope", "gstin", "pan", "business_name", "business_address", "address_proof")) {
            val requirement = SellerRequirement.from(key)
            assertThat(requirement).isNotInstanceOf(SellerRequirement.Unknown::class.java)
            assertThat(requirement.label()).isNotEqualTo(SellerRequirement.Unknown(key).label())
        }
    }

    // ─── The step ────────────────────────────────────────────────────

    private class StepApi(
        private val view: SellScopeViewDto? = null,
        private val answer: () -> Response<ApiEnvelope<SellScopeSavedDto>> = {
            envelope(SellScopeSavedDto(sellScope = "books_only", businessName = "Asha Books", panMasked = "XXXXXX234F", panHolderType = "INDIVIDUAL"))
        },
    ) : FakeCommerceApi() {
        val saves = mutableListOf<SaveSellScopeRequest>()

        override suspend fun sellScope(): Response<ApiEnvelope<SellScopeViewDto>> = view?.let { envelope(it) } ?: unused()

        override suspend fun saveSellScope(body: SaveSellScopeRequest): Response<ApiEnvelope<SellScopeSavedDto>> {
            saves += body
            return answer()
        }
    }

    @Test
    fun `the step checks the form first and sends nothing until it is right`() = runTest(dispatcher) {
        val api = StepApi()
        val vm = SellScopeViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        // Before a first Save nothing is flagged.
        assertThat(vm.state.value.errorFor(SellScopeField.GSTIN)).isNull()
        vm.save()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.saves).isEmpty()
        assertThat(vm.state.value.errorFor(SellScopeField.GSTIN)).isEqualTo("Enter your GSTIN.")
    }

    @Test
    fun `the books step sends the PAN and the business name and shows the PAN back masked`() = runTest(dispatcher) {
        val api = StepApi()
        val vm = SellScopeViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.setGstin("29AAACM1234A1Z4")
        vm.choose(SellScope.BOOKS_ONLY)
        vm.setPan("abcpe 1234f")
        vm.setBusinessName("Asha Books")
        vm.save()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.saves.single())
            .isEqualTo(SaveSellScopeRequest(sellScope = "books_only", pan = "ABCPE1234F", businessName = "Asha Books"))
        val saved = checkNotNull(vm.state.value.saved)
        assertThat(sellScopeSavedLine(saved)).isEqualTo("PAN XXXXXX234F · Individual")
    }

    @Test
    fun `a PAN the server refuses is said under the PAN field`() = runTest(dispatcher) {
        val api = StepApi(answer = { refusal("onboarding_sell_scope_put_400_pan_invalid.json") })
        val vm = SellScopeViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.choose(SellScope.BOOKS_ONLY)
        vm.setPan("ABCPE1234F")
        vm.setBusinessName("Asha Books")
        vm.save()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(vm.state.value.errorFor(SellScopeField.PAN)).isEqualTo("That is not a valid PAN.")
        assertThat(vm.state.value.error).isNull()
        assertThat(vm.state.value.saved).isNull()
    }

    @Test
    fun `a shop that chose before starts on its choice`() = runTest(dispatcher) {
        val view = SellScopeViewDto(sellScope = "books_only", bookCategoryIds = listOf("c-books"), canUpgrade = true)
        val vm = SellScopeViewModel(CommerceRepository(StepApi(view = view)))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(vm.state.value.draft.scope).isEqualTo(SellScope.BOOKS_ONLY)
    }

    // ─── The hub: Sell more categories ───────────────────────────────

    private class HubApi(
        private val scope: String,
        private val upgradeAnswer: () -> Response<ApiEnvelope<SellScopeViewDto>> = {
            envelope(SellScopeViewDto(sellScope = "all_categories", hasGstin = true, bookCategoryIds = listOf("c-books")))
        },
    ) : FakeCommerceApi() {
        var scopeReads = 0
        val upgrades = mutableListOf<UpgradeSellScopeRequest>()

        override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> =
            envelope(SellerProfileDto(id = "s-1", storeName = "Asha Books", status = "approved", sellScope = scope))

        override suspend fun sellerProducts(status: String?, limit: Int, offset: Int): Response<ApiEnvelope<SellerProductsDto>> =
            envelope(SellerProductsDto())

        override suspend fun sellScope(): Response<ApiEnvelope<SellScopeViewDto>> {
            scopeReads++
            return envelope(SellScopeViewDto(sellScope = "books_only", bookCategoryIds = listOf("c-books"), canUpgrade = true))
        }

        override suspend fun upgradeSellScope(body: UpgradeSellScopeRequest): Response<ApiEnvelope<SellScopeViewDto>> {
            upgrades += body
            return upgradeAnswer()
        }
    }

    private fun SellerViewModel.content() = state.value as SellerUiState.Content

    @Test
    fun `only a books-only shop is offered Sell more categories, and only it reads its scope`() = runTest(dispatcher) {
        val books = HubApi(scope = "books_only")
        val booksHub = SellerViewModel(CommerceRepository(books))
        val everything = HubApi(scope = "all_categories")
        val everythingHub = SellerViewModel(CommerceRepository(everything))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(offersUpgrade(booksHub.content().sellScope)).isTrue()
        assertThat(offersUpgrade(everythingHub.content().sellScope)).isFalse()
        assertThat(everything.scopeReads).isEqualTo(0)
    }

    @Test
    fun `adding the shop's GSTIN sells everything and the panel goes`() = runTest(dispatcher) {
        val api = HubApi(scope = "books_only")
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.openUpgrade()
        vm.setUpgradeGstin("27abcpe1234f1zb")
        vm.upgrade()
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.upgrades.single()).isEqualTo(UpgradeSellScopeRequest("27ABCPE1234F1ZB"))
        assertThat(vm.content().profile.sellScope).isEqualTo(SellScope.ALL_CATEGORIES)
        assertThat(offersUpgrade(vm.content().sellScope)).isFalse()
        assertThat(vm.content().message).isEqualTo(UPGRADE_DONE_COPY)
    }

    @Test
    fun `a GSTIN with a typo is caught before the round trip, another PAN's is refused in words`() = runTest(dispatcher) {
        val api = HubApi(
            scope = "books_only",
            upgradeAnswer = { refusal("seller_sell_scope_upgrade_post_400_gstin_pan_mismatch.json") },
        )
        val vm = SellerViewModel(CommerceRepository(api))
        dispatcher.scheduler.advanceUntilIdle()

        vm.openUpgrade()
        vm.setUpgradeGstin("27ABCPE1234F1ZC")
        vm.upgrade()
        dispatcher.scheduler.advanceUntilIdle()
        assertThat(api.upgrades).isEmpty()
        assertThat(vm.content().upgrade.error).contains("does not match")

        vm.setUpgradeGstin("27PQRCS6789Z1Z0")
        vm.upgrade()
        dispatcher.scheduler.advanceUntilIdle()
        assertThat(api.upgrades).hasSize(1)
        assertThat(vm.content().upgrade.error).contains("registered to a different PAN")
        assertThat(offersUpgrade(vm.content().sellScope)).isTrue()
    }

    // ─── The listing editor ──────────────────────────────────────────

    private class ListingApi(private val scope: String) : FakeCommerceApi() {
        override suspend fun taxClasses(): Response<ApiEnvelope<TaxClassListDto>> = envelope(
            TaxClassListDto(
                listOf(
                    TaxClassDto(id = "tax-0", name = "GST 0%", ratePercent = 0.0),
                    TaxClassDto(id = "tax-18", name = "GST 18%", ratePercent = 18.0),
                ),
            ),
        )

        override suspend fun categoryTree(): Response<ApiEnvelope<List<CategoryTreeDto>>> = envelope(
            listOf(
                CategoryTreeDto(id = "c-phones", name = "Phones", isListable = true),
                CategoryTreeDto(
                    id = "c-books-and-stationery",
                    name = "Books & Stationery",
                    isListable = false,
                    children = listOf(CategoryTreeDto(id = "c-books", name = "Textbooks", isListable = true)),
                ),
            ),
        )

        override suspend fun sellScope(): Response<ApiEnvelope<SellScopeViewDto>> =
            envelope(SellScopeViewDto(sellScope = scope, bookCategoryIds = listOf("c-books"), canUpgrade = scope == "books_only"))
    }

    @Test
    fun `a books-only shop is offered book categories at GST 0 percent, any other shop everything`() = runTest(dispatcher) {
        val books = NewProductViewModel(CommerceRepository(ListingApi("books_only")))
        val everything = NewProductViewModel(CommerceRepository(ListingApi("all_categories")))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(books.form.value.booksOnly).isTrue()
        assertThat(books.form.value.offeredCategories.map { it.id }).containsExactly("c-books")
        assertThat(books.form.value.offeredTaxClasses.map { it.id }).containsExactly("tax-0")
        assertThat(BOOKS_ONLY_EDITOR_NOTE).contains("GST 0%")

        assertThat(everything.form.value.offeredCategories.map { it.id }).containsExactly("c-phones", "c-books")
        assertThat(everything.form.value.offeredTaxClasses.map { it.id }).containsExactly("tax-0", "tax-18")
    }

    // ─── The proof of address ────────────────────────────────────────

    private class DocumentApi : FakeCommerceApi() {
        val saves = mutableListOf<SaveDocumentsRequest>()

        override suspend fun saveDocuments(body: SaveDocumentsRequest): Response<ApiEnvelope<Unit>> {
            saves += body
            return envelope(Unit)
        }
    }

    private fun uploader(vararg mediaIds: String): MediaUploader {
        val ids = ArrayDeque(mediaIds.toList())
        val uploader = mockk<MediaUploader>()
        coEvery { uploader.reserve(any(), any(), any(), any(), any()) } answers {
            AppResult.Success(MediaInitDto(mediaId = ids.removeFirst(), uploadUrl = "https://upload.test/put"))
        }
        coEvery { uploader.upload(any(), any(), any(), any(), any()) } returns PresignedPutResult.Success
        coEvery { uploader.confirm(any()) } returns AppResult.Success(mockk<MediaAssetDto>(relaxed = true))
        return uploader
    }

    private val sources = MediaSourceResolver { uri ->
        PickedMedia(uri = uri, mimeType = "image/jpeg", sizeBytes = 1_000L, source = mockk(relaxed = true))
    }

    @Test
    fun `a voter ID is attached only with its back, both sides in one document`() = runTest(dispatcher) {
        val api = DocumentApi()
        val vm = DocumentViewModel(CommerceRepository(api), uploader("front-1", "back-1"), sources)
        vm.startAsAddressProof()
        assertThat(vm.state.value.type).isEqualTo(SellerDocumentType.VOTER_ID)
        var attached = 0

        vm.upload("content://front") { attached++ }
        dispatcher.scheduler.advanceUntilIdle()
        assertThat(api.saves).isEmpty()
        assertThat(vm.state.value.awaitingBack).isTrue()
        assertThat(vm.state.value.pickLabel).isEqualTo("Choose the back")
        assertThat(documentGuidance(vm.state.value)).contains("Now send the back")

        vm.upload("content://back") { attached++ }
        dispatcher.scheduler.advanceUntilIdle()

        val document = api.saves.single().documents.single()
        assertThat(document.documentType).isEqualTo("voter_id")
        assertThat(document.mediaId).isEqualTo("front-1")
        assertThat(document.backMediaId).isEqualTo("back-1")
        assertThat(attached).isEqualTo(1)
        assertThat(vm.state.value.awaitingBack).isFalse()
    }

    @Test
    fun `a bill attaches at once, with no number and no back`() = runTest(dispatcher) {
        val api = DocumentApi()
        val vm = DocumentViewModel(CommerceRepository(api), uploader("bill-1"), sources)
        vm.setType(SellerDocumentType.ELECTRICITY_BILL)
        vm.setNumber("0012345678")

        assertThat(vm.state.value.asksForNumber).isFalse()
        vm.upload("content://bill") {}
        dispatcher.scheduler.advanceUntilIdle()

        val document = api.saves.single().documents.single()
        assertThat(document.documentType).isEqualTo("electricity_bill")
        assertThat(document.documentNumber).isNull()
        assertThat(document.backMediaId).isNull()
    }

    @Test
    fun `the proof-of-address screen offers only the five kinds that prove one`() {
        assertThat(documentChoices(addressProof = true)).containsExactly(
            SellerDocumentType.VOTER_ID,
            SellerDocumentType.PASSPORT,
            SellerDocumentType.ELECTRICITY_BILL,
            SellerDocumentType.TELEPHONE_BILL,
            SellerDocumentType.BANK_STATEMENT,
        ).inOrder()
        assertThat(documentChoices(addressProof = false)).containsAtLeast(SellerDocumentType.PAN_CARD, SellerDocumentType.VOTER_ID)
    }

    private fun newProduct() = com.us.android.core.commerce.model.NewProduct(
        title = "Momentum Power Bank 10000",
        description = null,
        taxClassId = "tax-18",
        sku = "MOM-PB-10K",
        mrp = com.us.android.core.commerce.model.Paise(249_900),
        sellingPrice = com.us.android.core.commerce.model.Paise(199_900),
        openingStock = 5,
        categoryId = "c-phones",
    )
}
