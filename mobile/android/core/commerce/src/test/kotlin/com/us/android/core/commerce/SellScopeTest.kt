package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.ADDRESS_PROOF_TYPES
import com.us.android.core.commerce.model.CategoryChoice
import com.us.android.core.commerce.model.SellScope
import com.us.android.core.commerce.model.SellScopeChoice
import com.us.android.core.commerce.model.SellScopeDraft
import com.us.android.core.commerce.model.SellScopeField
import com.us.android.core.commerce.model.SellerAddress
import com.us.android.core.commerce.model.SellerAddressKind
import com.us.android.core.commerce.model.SellerDocument
import com.us.android.core.commerce.model.SellerDocumentType
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.core.commerce.model.TaxClass
import com.us.android.core.commerce.model.bookCategoryChoices
import com.us.android.core.commerce.model.gstinCheckCharacter
import com.us.android.core.commerce.model.gstinPan
import com.us.android.core.commerce.model.gstinProblem
import com.us.android.core.commerce.model.imageOnly
import com.us.android.core.commerce.model.panHolderLabel
import com.us.android.core.commerce.model.panProblem
import com.us.android.core.commerce.model.provesAddress
import com.us.android.core.commerce.model.sellScopeChoice
import com.us.android.core.commerce.model.sellScopeProblems
import com.us.android.core.commerce.model.twoSided
import com.us.android.core.commerce.model.zeroRatedTaxClasses
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Books-only sellers (commerce-service migration 045, founder 24 Oct 2026):
 * the step's rules as the app checks them before the round trip, and what it
 * puts on the wire.
 */
class SellScopeTest {

    // ─── The PAN and the GSTIN ───────────────────────────────────────

    @Test
    fun `a PAN is five letters, four digits and a letter, with a real holder type`() {
        assertThat(panProblem("ABCPE1234F")).isNull()
        assertThat(panProblem(" abcpe 1234f ")).isNull()
        assertThat(panProblem("")).isEqualTo("Enter your PAN.")
        assertThat(panProblem("ABCPE1234")).isNotNull()
        assertThat(panProblem("ABC1E1234F")).isNotNull()
        // The fourth letter says who holds it; X is no holder type.
        assertThat(panProblem("ABCXE1234F")).isNotNull()
    }

    @Test
    fun `the server's holder type reads in words`() {
        assertThat(panHolderLabel("INDIVIDUAL")).isEqualTo("Individual")
        assertThat(panHolderLabel("HUF")).isEqualTo("Hindu undivided family")
        assertThat(panHolderLabel("NEW_KIND")).isEqualTo("New kind")
    }

    @Test
    fun `the fixtures' GSTINs pass the check character, and a typo does not`() {
        for (gstin in listOf("27ABCPE1234F1ZB", "27PQRCS6789Z1Z0", "29AAACM1234A1Z4")) {
            assertThat(gstinProblem(gstin)).isNull()
        }
        assertThat(gstinCheckCharacter("27ABCPE1234F1Z")).isEqualTo('B')
        assertThat(gstinProblem("27ABCPE1234F1ZC")).isEqualTo("The last character does not match; check the GSTIN for a typo.")
        assertThat(gstinProblem("27ABCPE1234F1")).isNotNull()
        assertThat(gstinProblem("")).isEqualTo("Enter your GSTIN.")
        assertThat(gstinPan("27ABCPE1234F1ZB")).isEqualTo("ABCPE1234F")
    }

    // ─── The step ────────────────────────────────────────────────────

    @Test
    fun `all categories needs a GSTIN, and the business name stays optional`() {
        val draft = SellScopeDraft(scope = SellScope.ALL_CATEGORIES, pan = "ABCPE1234F")
        assertThat(sellScopeProblems(draft).keys).containsExactly(SellScopeField.GSTIN)
        assertThat(sellScopeProblems(draft.copy(gstin = "29AAACM1234A1Z4"))).isEmpty()
        assertThat(sellScopeProblems(draft.copy(gstin = "29AAACM1234A1Z4", businessName = "A")).keys)
            .containsExactly(SellScopeField.BUSINESS_NAME)
    }

    @Test
    fun `books only needs a PAN and a business name, and no GSTIN`() {
        val draft = SellScopeDraft(scope = SellScope.BOOKS_ONLY, gstin = "29AAACM1234A1Z4")
        assertThat(sellScopeProblems(draft).keys).containsExactly(SellScopeField.PAN, SellScopeField.BUSINESS_NAME)
        assertThat(sellScopeProblems(draft.copy(pan = "ABCPE1234F", businessName = "Asha Books"))).isEmpty()
    }

    @Test
    fun `only the fields the chosen path takes are sent`() {
        val typed = SellScopeDraft(gstin = " 29aaacm1234a1z4 ", pan = "abcpe1234f", businessName = "  Asha Books ")

        assertThat(sellScopeChoice(typed.copy(scope = SellScope.ALL_CATEGORIES)))
            .isEqualTo(SellScopeChoice(SellScope.ALL_CATEGORIES, gstin = "29AAACM1234A1Z4", businessName = "Asha Books"))
        assertThat(sellScopeChoice(typed.copy(scope = SellScope.BOOKS_ONLY)))
            .isEqualTo(SellScopeChoice(SellScope.BOOKS_ONLY, pan = "ABCPE1234F", businessName = "Asha Books"))
    }

    @Test
    fun `an unknown scope is neither`() {
        assertThat(SellScope.fromWire("books_only")).isEqualTo(SellScope.BOOKS_ONLY)
        assertThat(SellScope.fromWire("all_categories")).isEqualTo(SellScope.ALL_CATEGORIES)
        assertThat(SellScope.fromWire("everything")).isNull()
        assertThat(SellScope.fromWire(null)).isNull()
    }

    @Test
    fun `the readiness codes of both scopes are known`() {
        assertThat(
            listOf("sell_scope", "gstin", "pan", "business_name", "business_address", "address_proof")
                .map(SellerRequirement::from),
        ).containsExactly(
            SellerRequirement.SellScopeNotChosen,
            SellerRequirement.Gstin,
            SellerRequirement.Pan,
            SellerRequirement.BusinessName,
            SellerRequirement.BusinessAddress,
            SellerRequirement.AddressProof,
        ).inOrder()
    }

    // ─── Listing ─────────────────────────────────────────────────────

    @Test
    fun `a books-only shop is offered book categories and GST 0 percent only`() {
        val choices = listOf(
            CategoryChoice("c-phones", "Electronics › Phones"),
            CategoryChoice("c-textbooks", "Books › Textbooks"),
        )
        assertThat(bookCategoryChoices(choices, listOf("c-textbooks", "c-elsewhere")))
            .containsExactly(CategoryChoice("c-textbooks", "Books › Textbooks"))

        val classes = listOf(TaxClass("t0", "GST 0%", 0.0), TaxClass("t5", "GST 5%", 5.0), TaxClass("t18", "GST 18%", 18.0))
        assertThat(zeroRatedTaxClasses(classes).map { it.id }).containsExactly("t0")
    }

    // ─── Proof of address ────────────────────────────────────────────

    @Test
    fun `a voter ID and a passport need their back, a bill and a statement carry no number`() {
        assertThat(SellerDocumentType.entries.filter { it.twoSided })
            .containsExactly(SellerDocumentType.VOTER_ID, SellerDocumentType.PASSPORT)
        assertThat(SellerDocumentType.entries.filter { it.imageOnly }).containsExactly(
            SellerDocumentType.ELECTRICITY_BILL,
            SellerDocumentType.TELEPHONE_BILL,
            SellerDocumentType.BANK_STATEMENT,
        )
        assertThat(SellerDocumentType.entries.filter { it.provesAddress }).containsExactlyElementsIn(ADDRESS_PROOF_TYPES)
        assertThat(SellerDocumentType.ADDRESS_PROOF.provesAddress).isFalse()
    }

    // ─── The wire ────────────────────────────────────────────────────

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repo: CommerceRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CommerceApi::class.java)
        repo = CommerceRepository(api)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String = """{"data":{}}""") {
        server.enqueue(MockResponse.Builder().code(200).body(body).build())
    }

    @Test
    fun `the books step puts the PAN and the business name, and no GSTIN key`() {
        enqueue("""{"data":{"sell_scope":"books_only","business_name":"Asha Books","pan_masked":"XXXXXX234F","pan_holder_type":"INDIVIDUAL"}}""")

        runBlocking { repo.saveSellScope(SellScopeChoice(SellScope.BOOKS_ONLY, gstin = "29AAACM1234A1Z4", pan = "ABCPE1234F", businessName = "Asha Books")) }

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("PUT")
        assertThat(request.target).isEqualTo("/v1/commerce/onboarding/step/sell-scope")
        assertThat(request.body!!.utf8())
            .isEqualTo("""{"sell_scope":"books_only","pan":"ABCPE1234F","business_name":"Asha Books"}""")
    }

    @Test
    fun `the all-categories step puts the GSTIN and no PAN key`() {
        enqueue("""{"data":{"sell_scope":"all_categories","gstin":"29AAACM1234A1Z4"}}""")

        runBlocking { repo.saveSellScope(SellScopeChoice(SellScope.ALL_CATEGORIES, gstin = "29AAACM1234A1Z4", pan = "ABCPE1234F")) }

        assertThat(server.takeRequest().body!!.utf8()).isEqualTo("""{"sell_scope":"all_categories","gstin":"29AAACM1234A1Z4"}""")
    }

    @Test
    fun `the scope is read and upgraded on the seller path`() {
        val view = """{"data":{"sell_scope":"books_only","has_gstin":false,"book_category_ids":["c1"],"can_upgrade":true}}"""
        enqueue(view)
        enqueue(view)

        runBlocking { repo.sellScope() }
        runBlocking { repo.upgradeSellScope(" 27abcpe1234f1zb ") }

        val read = server.takeRequest()
        assertThat(read.method).isEqualTo("GET")
        assertThat(read.target).isEqualTo("/v1/commerce/seller/sell-scope")
        val upgrade = server.takeRequest()
        assertThat(upgrade.method).isEqualTo("POST")
        assertThat(upgrade.target).isEqualTo("/v1/commerce/seller/sell-scope/upgrade")
        assertThat(upgrade.body!!.utf8()).isEqualTo("""{"gstin":"27ABCPE1234F1ZB"}""")
    }

    @Test
    fun `a back side goes only with a voter ID or passport, a number never with a bill`() {
        enqueue()

        runBlocking {
            repo.saveDocuments(
                listOf(
                    SellerDocument(SellerDocumentType.VOTER_ID, "front-1", "ABC1234567", backMediaId = "back-1"),
                    SellerDocument(SellerDocumentType.BANK_STATEMENT, "front-2", "0012345678", backMediaId = "back-2"),
                ),
            )
        }

        assertThat(server.takeRequest().body!!.utf8()).isEqualTo(
            """{"documents":[""" +
                """{"document_type":"voter_id","media_id":"front-1","document_number":"ABC1234567","back_media_id":"back-1"},""" +
                """{"document_type":"bank_statement","media_id":"front-2"}]}""",
        )
    }

    @Test
    fun `a business address is the business row and never the default pickup`() {
        enqueue()
        val address = SellerAddress("Asha", "9000000001", "1 Book Street", null, "Pune", "MH", "411001")

        runBlocking { repo.saveSellerAddress(address, SellerAddressKind.BUSINESS) }

        val body = server.takeRequest().body!!.utf8()
        assertThat(body).contains(""""address_type":"business"""")
        assertThat(body).contains(""""is_default":false""")
    }
}
