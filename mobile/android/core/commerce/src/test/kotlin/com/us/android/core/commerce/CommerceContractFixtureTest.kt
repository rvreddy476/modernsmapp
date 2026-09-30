package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.NewProduct
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderPage
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.ProductImage
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.network.AddressDto
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CategoryDto
import com.us.android.core.commerce.network.CategoryTreeDto
import com.us.android.core.commerce.network.CheckoutResultDto
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.FavouriteDto
import com.us.android.core.commerce.network.HomeDto
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderPaymentDto
import com.us.android.core.commerce.network.PaymentHandleDto
import com.us.android.core.commerce.network.PaymentStatusDto
import com.us.android.core.commerce.network.ProductDetailDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.QuoteDto
import com.us.android.core.commerce.network.ReadinessDto
import com.us.android.core.commerce.network.SellerFulfilmentResultDto
import com.us.android.core.commerce.network.SellerOrderCardDto
import com.us.android.core.commerce.network.SellerOrderDto
import com.us.android.core.commerce.network.SellerOrderHistoryDto
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.ShipmentsDto
import com.us.android.core.commerce.network.StockDto
import com.us.android.core.commerce.network.TaxClassListDto
import com.us.android.core.commerce.network.VariantListDto
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.commerce.repository.isPermanentRefusal
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.Assume.assumeTrue
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.File

/**
 * Every golden contract fixture from commerce-service that the app reads
 * decodes into its DTO, and every refusal maps to the error a screen
 * branches on.
 *
 * The fixtures are commerce-service's handler-test goldens
 * (`internal/http/testdata/contracts/<area>/<name>.json`, lane C1), copied
 * byte for byte into `src/test/resources/contracts/<area>/`. Only the ones a
 * route in [CommerceApi] answers are copied (56 of 67 on 2026-09-30); the
 * invoice, order items, shipment timeline, reviews, attributes, product
 * readiness, onboarding status and stub-confirm goldens belong to screens
 * lanes AC2 and AC3 have not built, and are copied with the DTOs that read
 * them.
 *
 * Three kinds of check:
 *  * [strict] — `ignoreUnknownKeys = false`, where the DTO declares the
 *    whole shape the server sends. A key renamed on either side, or one the
 *    server added, fails here rather than defaulting silently in production.
 *  * [lenient] — the app's own Json, where the server's row is a wide
 *    Postgres projection the DTO deliberately reads only part of (rupee
 *    mirrors, timestamps, ids the app never uses). The checks name the
 *    fields the app DOES read, so a rename of one of those still fails.
 *  * [served] — the fixture is answered by a MockWebServer at the status in
 *    its name and read through [CommerceRepository], so the refusal is
 *    pinned as the [CommerceError] a screen sees, and an empty-bodied 204 as
 *    a success.
 *
 * [parsers] must name every fixture as `<area>/<name>.json`: a fixture
 * without a parser, or a parser without its fixture, fails the coverage test.
 *
 * Fixtures that revealed DTO bugs (2026-09-30), each fixed in the DTO:
 * `product_media_200` (the list is `items`, the DTO read `media`, so every
 * gallery was empty); `order_get_200_*` (lines carry `line_total_minor`, the
 * DTO read `final_price_minor`, so every line on an order read ₹0);
 * `orders_list_200` (rows carry `item_count` and `first_product_title`, not
 * `items`, so a list row named nothing); `checkout_v2_post_409_amount_mismatch`
 * (`lines: null` threw inside the error mapping and the refusal arrived as a
 * network failure); `checkout_v2_post_409_price_changed` (`new_total_minor: 0`
 * rendered as a new total of ₹0).
 */
class CommerceContractFixtureTest {

    private val strictJson = Json { ignoreUnknownKeys = false }
    private val appJson = NetworkModule.provideJson()

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (name: String, raw: String) -> Unit> = mapOf(
        // ── Addresses ──────────────────────────────────────────────────
        "addresses/addresses_list_200.json" to lenient(ListSerializer(AddressDto.serializer())) {
            assertThat(it.map { a -> a.id }).containsExactly(
                "00000000-0000-4000-8000-0000000c0021",
                "00000000-0000-4000-8000-0000000c0022",
            ).inOrder()
            assertThat(it.first().isDefault).isTrue()
            assertThat(it.first().line1).isEqualTo("5 Main Street")
            assertThat(it.first().postalCode).isEqualTo("560002")
            assertThat(it.last().line2).isNull()
        },
        "addresses/address_post_201.json" to lenient(AddressDto.serializer()) {
            assertThat(it.id).isEqualTo("00000000-0000-4000-8000-0000000f0005")
            assertThat(it.contactName).isEqualTo("Ravi Buyer")
            // No `label` on the wire for this row: the DTO's default holds.
            assertThat(it.label).isEqualTo("Home")
        },

        // ── Bag ────────────────────────────────────────────────────────
        "bag/cart_get_200.json" to lenient(CartDto.serializer()) {
            assertThat(it.items).hasSize(3)
            assertThat(it.subtotalMinor).isEqualTo(Paise(389_600))
            assertThat(it.items.first().priceWasMinor).isEqualTo(Paise(45_900))
            assertThat(it.items[1].sellable).isFalse()
            assertThat(it.items.last().lineTotalMinor).isEqualTo(Paise(259_800))
        },
        "bag/cart_get_200_empty.json" to strict(CartDto.serializer()) {
            assertThat(it.items).isEmpty()
            assertThat(it.itemCount).isEqualTo(0)
        },
        "bag/cart_item_post_200.json" to lenient(CartDto.serializer()) {
            assertThat(it.itemCount).isEqualTo(4)
            assertThat(it.sellerName).isEqualTo("Momentum Electronics")
        },
        "bag/cart_item_post_409_multiple_sellers.json" to served({ addToCart("v-1", 1) }) {
            assertThat(it.error()).isEqualTo(CommerceError.MultipleSellers)
        },
        "bag/cart_item_post_409_out_of_stock.json" to served({ addToCart("v-1", 1) }) {
            val line = (it.error() as CommerceError.OutOfStock).lines.single()
            assertThat(line.variantId).isEqualTo("00000000-0000-4000-8000-0000000c0102")
            assertThat(line.requested).isEqualTo(1)
            assertThat(line.available).isEqualTo(0)
        },
        "bag/cart_item_post_409_unavailable.json" to served({ addToCart("v-1", 1) }) {
            assertThat(it.error()).isEqualTo(CommerceError.ProductUnavailable)
        },

        // ── Quote and checkout ─────────────────────────────────────────
        "checkout/quote_post_200.json" to strict(QuoteDto.serializer()) {
            assertThat(it.quoteId).isEqualTo("00000000-0000-4000-8000-0000000f000b")
            assertThat(it.totalMinor).isEqualTo(Paise(264_700))
            assertThat(it.taxMinor).isEqualTo(Paise(40_378))
            assertThat(it.serviceable).isTrue()
        },
        "checkout/quote_post_400_payment_method.json" to served({ quote("a-1", paymentMethod = "cod") }) {
            val error = it.error() as CommerceError.Unexpected
            assertThat(error.code).isEqualTo("PAYMENT_METHOD_NOT_SUPPORTED")
            assertThat(error.httpStatus).isEqualTo(400)
        },
        "checkout/quote_post_422_not_serviceable.json" to served({ quote("a-1") }) {
            assertThat(it.error()).isEqualTo(CommerceError.NotServiceable("pincode not serviceable"))
        },
        "checkout/checkout_v2_post_201.json" to strict(CheckoutResultDto.serializer()) {
            assertThat(it.orderId).isEqualTo("00000000-0000-4000-8000-0000000f000c")
            assertThat(it.totalMinor).isEqualTo(Paise(134_800))
            assertThat(it.paymentIntentId).isEqualTo("00000000-0000-4000-8000-0000000f000d")
            assertThat(it.clientSession).isNull()
        },
        "checkout/checkout_v2_post_400_missing_idempotency_key.json" to served({ checkoutOnce() }) {
            assertThat((it.error() as CommerceError.Unexpected).code).isEqualTo("IDEMPOTENCY_KEY_REQUIRED")
        },
        "checkout/checkout_v2_post_409_amount_mismatch.json" to served({ checkoutOnce() }) {
            // `lines` is JSON null here; this used to surface as a Network error.
            assertThat(it.error()).isEqualTo(CommerceError.PriceChanged(lines = emptyList(), newTotal = Paise(134_800)))
        },
        "checkout/checkout_v2_post_409_price_changed.json" to served({ checkoutOnce() }) {
            val error = it.error() as CommerceError.PriceChanged
            val line = error.lines.single()
            assertThat(line.variantId).isEqualTo("00000000-0000-4000-8000-0000000c0101")
            assertThat(line.was).isEqualTo(Paise(119_900))
            assertThat(line.now).isEqualTo(Paise(129_900))
            // `new_total_minor: 0` is Go's "none", never a total of ₹0.
            assertThat(error.newTotal).isNull()
        },
        "checkout/checkout_v2_post_409_quote_stale.json" to served({ checkoutOnce() }) {
            assertThat(it.error()).isEqualTo(CommerceError.QuoteStale)
        },

        // ── Orders ─────────────────────────────────────────────────────
        "orders/orders_list_200.json" to served({ orders() }) {
            val page = it.value<OrderPage>()
            assertThat(page.items.map { o -> o.orderNumber })
                .containsExactly("ORD-2026-010005", "ORD-2026-010004").inOrder()
            assertThat(page.nextCursor).isNotEmpty()
            // The list row names what was bought from the summary fields.
            assertThat(page.items.first().firstLineTitle).isEqualTo("Momentum Wireless Earbuds")
            assertThat(page.items.first().lineCount).isEqualTo(1)
            assertThat(page.items.first().canPayNow).isTrue()
        },
        "orders/order_get_200_confirmed.json" to lenient(OrderDto.serializer()) {
            assertThat(it.status).isEqualTo("confirmed")
            assertThat(it.canRetryPayment).isFalse()
            assertThat(it.items.single().total).isEqualTo(Paise(129_900))
            assertThat(it.deliveryAddress?.city).isEqualTo("Bengaluru")
        },
        "orders/order_get_200_delivered.json" to served({ order("o-1") }) {
            val order = it.value<Order>()
            assertThat(order.status).isEqualTo(OrderStatus.DELIVERED)
            assertThat(order.lines.map { l -> l.lineTotal }).containsExactly(Paise(129_900), Paise(129_900))
            assertThat(order.trackingUrl).isEqualTo("https://example.test/track/CT0C0210")
            assertThat(order.canPayNow).isFalse()
        },
        "orders/order_get_200_payment_failed.json" to served({ order("o-1") }) {
            val order = it.value<Order>()
            assertThat(order.status).isEqualTo(OrderStatus.PAYMENT_FAILED)
            assertThat(order.canRetryPayment).isTrue()
            assertThat(order.canPayNow).isTrue()
        },
        "orders/order_cancel_post_204.json" to served({ cancelOrder("o-1", "Changed my mind") }) {
            assertThat(it).isEqualTo(CommerceResult.Success(Unit))
        },
        "orders/order_cancel_post_404_not_owner.json" to served({ cancelOrder("o-1", "x") }) {
            // A coded 404 is a refusal, not "this server has no such route".
            assertThat(it.error()).isEqualTo(CommerceError.OrderNotFound)
            assertThat(it.error().isPermanentRefusal()).isTrue()
        },
        "orders/order_cancel_post_409_not_permitted.json" to served({ cancelOrder("o-1", "x") }) {
            assertThat(it.error()).isEqualTo(CommerceError.CancelNotPermitted)
        },

        // ── Payment ────────────────────────────────────────────────────
        "payment/order_payment_get_200_confirming.json" to strict(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("confirming")
            assertThat(it.refundStatus).isNull()
            assertThat(it.amountMinor).isEqualTo(Paise(134_800))
        },
        "payment/order_payment_get_200_paid.json" to strict(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
            assertThat(it.refundStatus).isNull()
        },
        "payment/order_payment_get_200_paid_after_stub_confirm.json" to strict(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
        },
        "payment/order_payment_get_200_failed.json" to strict(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("failed")
        },
        "payment/order_payment_get_200_paid_refund_pending.json" to strict(OrderPaymentDto.serializer()) {
            assertThat(it.status).isEqualTo("paid")
            assertThat(it.refundStatus).isEqualTo("pending")
        },
        "payment/order_payment_get_403_not_customer.json" to served({ orderPayment("o-1") }) {
            val error = it.error() as CommerceError.Unexpected
            assertThat(error.code).isEqualTo("FORBIDDEN")
            assertThat(error.httpStatus).isEqualTo(403)
            // The poll stops on this rather than asking for 180 s.
            assertThat(error.isPermanentRefusal()).isTrue()
        },
        "payment/payment_intent_post_200_razorpay.json" to strict(PaymentHandleDto.serializer()) {
            assertThat(it.paymentIntentId).isEqualTo("00000000-0000-4000-8000-0000000f000d")
            assertThat(it.amountMinor).isEqualTo(Paise(134_800))
            assertThat(it.clientSession).containsEntry("provider", "razorpay")
            assertThat(it.clientSession).containsEntry("key_id", "rzp_test_ContractKey01")
        },
        "payment/payment_intent_post_200_stub.json" to strict(PaymentHandleDto.serializer()) {
            assertThat(it.clientSession).containsEntry("provider", "stub")
            assertThat(it.status).isEqualTo("pending")
        },
        "payment/payment_status_get_200.json" to strict(PaymentStatusDto.serializer()) {
            assertThat(it.orderStatus).isEqualTo("payment_pending")
            assertThat(it.paymentStatus).isEqualTo("pending")
        },

        // ── Seller ─────────────────────────────────────────────────────
        "seller/sellers_me_200.json" to lenient(SellerProfileDto.serializer()) {
            assertThat(it.storeName).isEqualTo("Momentum Electronics")
            assertThat(it.status).isEqualTo("approved")
            assertThat(it.onboardingStep).isEqualTo(7)
        },
        "seller/onboarding_readiness_200.json" to lenient(ReadinessDto.serializer()) {
            assertThat(it.ready).isTrue()
            assertThat(it.missing).isEmpty()
        },
        "seller/onboarding_submit_409_incomplete.json" to served({ submitSellerApplication() }) {
            val error = it.error() as CommerceError.Unexpected
            assertThat(error.code).isEqualTo("APPLICATION_INCOMPLETE")
            assertThat(error.httpStatus).isEqualTo(409)
        },
        "seller/seller_products_200.json" to served({ sellerProducts() }) {
            val products = it.value<List<SellerProduct>>()
            assertThat(products).hasSize(4)
            // The variant-id fix rests on this: every row carries its
            // variants WITH THEIR OWN IDS, none of them the product's.
            for (product in products) {
                assertThat(product.variants).isNotEmpty()
                assertThat(product.variants.map { v -> v.id }).doesNotContain(product.id)
                assertThat(product.defaultVariantId).isEqualTo(product.variants.first().id)
            }
            val earbuds = products.single { p -> p.id == "00000000-0000-4000-8000-0000000c0100" }
            assertThat(earbuds.variants.map { v -> v.id }).containsExactly(
                "00000000-0000-4000-8000-0000000c0101",
                "00000000-0000-4000-8000-0000000c0102",
            ).inOrder()
            assertThat(earbuds.variants.last().availableQty).isEqualTo(0)
        },
        "seller/seller_variant_stock_get_200.json" to strict(StockDto.serializer()) {
            assertThat(it.variantId).isEqualTo("00000000-0000-4000-8000-0000000c0101")
            assertThat(it.available).isEqualTo(24)
        },
        "seller/product_post_201.json" to lenient(SellerProductDto.serializer()) {
            assertThat(it.id).isEqualTo("00000000-0000-4000-8000-0000000f000e")
            assertThat(it.status).isEqualTo("draft")
            assertThat(it.approvalStatus).isEqualTo("draft")
        },
        "seller/product_post_400_tax_class_required.json" to served({ createProduct(newProduct()) }) {
            val error = it.error() as CommerceError.Unexpected
            assertThat(error.code).isEqualTo("TAX_CLASS_REQUIRED")
            assertThat(error.httpStatus).isEqualTo(400)
        },
        "seller/product_submit_post_204.json" to served({ submitProduct("p-1") }) {
            assertThat(it).isEqualTo(CommerceResult.Success(Unit))
        },
        "seller/seller_orders_200.json" to lenient(ListSerializer(SellerOrderDto.serializer())) {
            assertThat(it).hasSize(3)
            assertThat(it[1].sellerSubtotalMinor).isEqualTo(Paise(129_900))
            assertThat(it[1].itemCount).isEqualTo(1)
        },
        "seller/seller_order_get_200.json" to lenient(SellerOrderCardDto.serializer()) {
            assertThat(it.order.id).isEqualTo("00000000-0000-4000-8000-0000000c0200")
            assertThat(it.items.single().finalPriceMinor).isEqualTo(Paise(129_900))
            assertThat(it.sellerSubtotalMinor).isEqualTo(Paise(129_900))
            assertThat(it.deliveryAddress).isNotEmpty()
        },
        "seller/seller_order_history_200.json" to lenient(SellerOrderHistoryDto.serializer()) {
            assertThat(it.history.map { h -> h.toStatus }).containsExactly("payment_pending", "confirmed").inOrder()
            assertThat(it.history.first().fromStatus).isNull()
        },
        "seller/seller_order_pack_post_200.json" to strict(SellerFulfilmentResultDto.serializer()) {
            assertThat(it.status).isEqualTo("packed")
            assertThat(it.applied).isTrue()
        },
        "seller/seller_order_ship_post_201.json" to strict(ShipmentsDto.serializer()) {
            val shipment = it.shipments.single()
            assertThat(shipment.trackingNumber).isEqualTo("CT0C0200")
            assertThat(shipment.deliveredAt).isNull()
        },

        // ── Storefront ─────────────────────────────────────────────────
        "storefront/categories_200.json" to lenient(ListSerializer(CategoryDto.serializer())) {
            assertThat(it).hasSize(12)
            assertThat(it.first().name).isEqualTo("Electronics")
            assertThat(it.first().productCount).isEqualTo(1)
        },
        "storefront/categories_tree_200.json" to strict(ListSerializer(CategoryTreeDto.serializer())) {
            val books = it.single { c -> c.slug == "books-and-stationery" }
            assertThat(books.isListable).isFalse()
            assertThat(books.children.single().name).isEqualTo("Textbooks")
            assertThat(books.children.single().isListable).isTrue()
        },
        "storefront/home_200.json" to lenient(HomeDto.serializer()) {
            assertThat(it.banners.map { b -> b.targetType }.toSet()).containsExactly("category")
            assertThat(it.sections.first().key).isEqualTo("deals")
            assertThat(it.sections.first().products.first().id).isEqualTo("00000000-0000-4000-8000-0000000c0110")
        },
        "storefront/products_list_200.json" to lenient(ProductListDto.serializer()) {
            assertThat(it.items).hasSize(3)
            assertThat(it.items.first().title).isEqualTo("Momentum Canvas Cap")
            // Go's "" is "no next page".
            assertThat(it.nextCursor).isEmpty()
        },
        "storefront/favourites_list_200.json" to lenient(ProductListDto.serializer()) {
            assertThat(it.items.single().isFavourite).isTrue()
        },
        "storefront/favourite_post_200.json" to strict(FavouriteDto.serializer()) {
            assertThat(it.isFavourite).isTrue()
        },
        "storefront/product_get_200.json" to lenient(ProductDetailDto.serializer()) {
            assertThat(it.product.id).isEqualTo("00000000-0000-4000-8000-0000000c0100")
            assertThat(it.product.sellerId).isEqualTo("00000000-0000-4000-8000-0000000c0010")
            assertThat(it.variants.map { v -> v.sellingPriceMinor }).containsExactly(Paise(129_900), Paise(129_900))
            assertThat(it.variants.last().availableQty).isEqualTo(0)
        },
        "storefront/product_get_404.json" to served({ product("p-1") }) {
            val error = it.error() as CommerceError.Unexpected
            assertThat(error.code).isEqualTo("PRODUCT_NOT_FOUND")
            assertThat(error.httpStatus).isEqualTo(404)
        },
        "storefront/product_variants_200.json" to lenient(VariantListDto.serializer()) {
            assertThat(it.items.map { v -> v.id }).containsExactly(
                "00000000-0000-4000-8000-0000000c0101",
                "00000000-0000-4000-8000-0000000c0102",
            ).inOrder()
        },
        "storefront/product_media_200.json" to served({ productImages("p-1") }) {
            val images = it.value<List<ProductImage>>()
            // The gallery is under `items`; the DTO read `media` and got nothing.
            assertThat(images.map { i -> i.mediaId }).containsExactly(
                "00000000-0000-4000-8000-0000000d0001",
                "00000000-0000-4000-8000-0000000d0002",
            ).inOrder()
        },
        "storefront/tax_classes_200.json" to strict(TaxClassListDto.serializer()) {
            assertThat(it.items.map { t -> t.ratePercent }).containsExactly(0.0, 5.0, 12.0, 18.0, 28.0).inOrder()
        },
    )

    private fun fixtures(): Set<String> =
        contractsDir.walkTopDown()
            .filter { it.isFile && it.name.endsWith(".json") }
            .map { it.relativeTo(contractsDir).path.replace(File.separatorChar, '/') }
            .toSet()

    @Test
    fun `every fixture has a parser and every parser has a fixture`() {
        assertThat(contractsDir.isDirectory).isTrue()
        val fixtures = fixtures()

        assertThat(fixtures).isNotEmpty()
        assertThat(fixtures - parsers.keys).isEmpty()
        assertThat(parsers.keys - fixtures).isEmpty()
    }

    @Test
    fun `every fixture decodes into its DTO and every refusal maps to its error`() {
        for ((name, parse) in parsers) {
            val raw = File(contractsDir, name).readText()
            try {
                parse(name, raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed: ${e.message}", e)
            }
        }
    }

    @Test
    fun `the copies are byte-identical to the commerce-service goldens`() {
        val source = File("../../../../Architecture/services/commerce-service/internal/http/testdata/contracts")
        assumeTrue("commerce-service is not checked out beside the app", source.isDirectory)

        for (name in fixtures()) {
            val original = File(source, name)
            assertThat(original.exists()).isTrue()
            assertThat(File(contractsDir, name).readBytes()).isEqualTo(original.readBytes())
        }
    }

    // ─── Parsers ─────────────────────────────────────────────────────

    /** Unknown keys FAIL: the DTO claims the whole shape. */
    private fun <T> strict(serializer: KSerializer<T>, check: (T) -> Unit) = decode(strictJson, serializer, check)

    /** The app's Json: the DTO reads part of a wider row. [check] names the parts it reads. */
    private fun <T> lenient(serializer: KSerializer<T>, check: (T) -> Unit) = decode(appJson, serializer, check)

    private fun <T> decode(json: Json, serializer: KSerializer<T>, check: (T) -> Unit): (String, String) -> Unit =
        { _, raw ->
            val envelope = json.decodeFromString(ApiEnvelope.serializer(serializer), raw)
            assertThat(envelope.error).isNull()
            check(checkNotNull(envelope.data))
        }

    /**
     * The fixture answered at the status in its name, read through the
     * repository — so the result is what a screen actually sees.
     */
    private fun served(
        call: suspend CommerceRepository.() -> CommerceResult<*>,
        check: (CommerceResult<*>) -> Unit,
    ): (String, String) -> Unit = { name, raw ->
        val status = checkNotNull(STATUS.find(name.substringAfterLast('/'))) {
            "fixture name $name carries no HTTP status"
        }.groupValues[1].toInt()
        MockWebServer().use { server ->
            server.start()
            server.enqueue(MockResponse.Builder().code(status).body(raw).build())
            val api = Retrofit.Builder()
                .baseUrl(server.url("/"))
                .addConverterFactory(appJson.asConverterFactory("application/json".toMediaType()))
                .build()
                .create(CommerceApi::class.java)
            check(runBlocking { CommerceRepository(api).call() })
        }
    }

    private suspend fun CommerceRepository.checkoutOnce() = checkout(
        idempotencyKey = "key-1",
        addressId = "a-1",
        quoteId = "q-1",
        paymentMethod = "upi",
        expectedTotal = Paise(134_800),
    )

    private fun newProduct() = NewProduct(
        title = "Momentum Power Bank 10000",
        description = null,
        taxClassId = "00000000-0000-4000-8000-0000000c0e18",
        sku = "MOM-PB-10K",
        mrp = Paise(249_900),
        sellingPrice = Paise(199_900),
        openingStock = 5,
        categoryId = "00000000-0000-4000-8000-00000000c001",
    )

    private fun CommerceResult<*>.error(): CommerceError =
        (this as? CommerceResult.Failure)?.error ?: throw AssertionError("expected a failure, got $this")

    @Suppress("UNCHECKED_CAST")
    private fun <T> CommerceResult<*>.value(): T =
        (this as? CommerceResult.Success<*>)?.value as? T ?: throw AssertionError("expected a success, got $this")

    private companion object {
        val STATUS = Regex("""_(\d{3})(?:_|\.json)""")
    }
}
