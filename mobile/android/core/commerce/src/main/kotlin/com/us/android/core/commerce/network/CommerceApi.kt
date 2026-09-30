package com.us.android.core.commerce.network

import com.us.android.core.commerce.model.Paise
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.KSerializer
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.descriptors.SerialDescriptor
import kotlinx.serialization.descriptors.buildClassSerialDescriptor
import kotlinx.serialization.encoding.Decoder
import kotlinx.serialization.encoding.Encoder
import kotlinx.serialization.json.JsonDecoder
import kotlinx.serialization.json.JsonEncoder
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The Commerce P0 endpoints.
 *
 * Two things about this interface are load-bearing rather than stylistic:
 *
 *  1. `checkout` takes an `Idempotency-Key` HEADER and the server rejects the
 *     request without one (LB-15). The old server fabricated a key when the
 *     client omitted it — a key that can never match a retry and therefore
 *     dedupes nothing, so the unique index existed but never fired.
 *
 *  2. There is no endpoint here that names a payment AMOUNT. The client asks
 *     to pay for an ORDER (LB-4). The removed `POST /v1/payments/intents`
 *     took an amount from the request body, which is how a buyer could open a
 *     1-paise intent against their own ₹10,000 order — and, with the equally
 *     removed `PATCH /intents/:id/status`, mark it succeeded without a single
 *     rupee reaching the PSP.
 */
@Suppress("TooManyFunctions")
interface CommerceApi {

    // ─── Catalog ─────────────────────────────────────────────────────

    @GET("v1/commerce/products")
    suspend fun listProducts(
        @Query("q") query: String? = null,
        @Query("category_id") categoryId: String? = null,
        @Query("cursor") cursor: String? = null,
        @Query("limit") limit: Int = 20,
        @Query("sort") sort: String? = null,
    ): Response<ApiEnvelope<ProductListDto>>

    @GET("v1/commerce/products/{productId}")
    suspend fun getProduct(
        @Path("productId") productId: String,
    ): Response<ApiEnvelope<ProductDetailDto>>

    /**
     * A product's variants, every status, in creation order. Public.
     *
     * The seller half reads this to find the VARIANT id a stock or price edit
     * addresses (2026-09-30). Variant ids are their own UUIDs, never the
     * product id: `GET /seller/variants/:id/stock` looks up `product_variants.id`,
     * and a product id sent there is a variant that does not exist.
     */
    @GET("v1/commerce/products/{productId}/variants")
    suspend fun productVariants(
        @Path("productId") productId: String,
    ): Response<ApiEnvelope<VariantListDto>>

    /**
     * The taxonomy, as a bare array in `data`.
     *
     * Not `{items: []}` — the handler writes the slice itself, and it writes
     * `[]` rather than `null` for an unseeded table, so the client's
     * "no categories" and "broken endpoint" stay distinguishable.
     */
    @GET("v1/commerce/categories")
    suspend fun categories(): Response<ApiEnvelope<List<CategoryDto>>>

    /**
     * The taxonomy NESTED, with `is_listable` and `children` on every node.
     *
     * A second route rather than a flag on [categories]: the flat answer is
     * byte-identical whether or not the parameter is sent, and the server
     * keeps it that way so two shipped clients keep decoding it. The tree is
     * what a category PICKER needs — a flat list cannot show that
     * "Textbooks" lives under "Books", and a seller choosing from it picks the
     * browse heading their listing may not sit on. A server that predates the
     * flag answers the flat shape, which fails to decode as a tree; the
     * repository then falls back to [categories] and derives the leaves.
     */
    @GET("v1/commerce/categories?tree=true")
    suspend fun categoryTree(): Response<ApiEnvelope<List<CategoryTreeDto>>>

    /**
     * The storefront's landing page: banners plus named product shelves.
     *
     * Additive and optional. A build that meets a server without it gets a
     * 404, and MStore falls back to categories plus the paged grid rather
     * than showing an empty shop — which is why the repository maps a bare
     * 404 to [com.us.android.core.commerce.repository.CommerceError.NotAvailable]
     * instead of a failure the screen would have to render.
     */
    @GET("v1/commerce/home")
    suspend fun home(): Response<ApiEnvelope<HomeDto>>

    /** The buyer's saved products. Server-owned: there is no local list. */
    @GET("v1/commerce/favourites")
    suspend fun favourites(): Response<ApiEnvelope<ProductListDto>>

    /**
     * Saves a product. The id travels in the BODY, not the path.
     *
     * This used to be `POST /favourites/{productId}`, a route commerce-service
     * never had, so every tap on the heart was a 404 that the optimistic UI
     * flipped back a moment later. The server's contract is
     * `POST /v1/commerce/favourites` with `{"product_id":"..."}`; the remove
     * stays path-addressed because that IS how the server declares it.
     *
     * Idempotent on the server: a second add is 200, not 409, so a double-tap
     * or a retried request never shows a failure for a state it reached.
     */
    @POST("v1/commerce/favourites")
    suspend fun addFavourite(
        @Body body: AddFavouriteRequest,
    ): Response<ApiEnvelope<FavouriteDto>>

    @DELETE("v1/commerce/favourites/{productId}")
    suspend fun removeFavourite(
        @Path("productId") productId: String,
    ): Response<ApiEnvelope<Unit>>

    /** A product's gallery, in the seller's chosen order. Public. */
    @GET("v1/commerce/products/{productId}/media")
    suspend fun productMedia(
        @Path("productId") productId: String,
    ): Response<ApiEnvelope<ProductMediaListDto>>

    /**
     * Attaches a whole gallery in one call, cover first.
     *
     * The order of [AttachProductMediaRequest.mediaIds] IS the gallery order,
     * so a reorder is one request rather than n. A server that predates the
     * batch shape answers 400/404 and the repository replays the list through
     * [attachOneProductMedia] — the older per-asset endpoint — so a seller's
     * images land either way.
     */
    @POST("v1/commerce/products/{productId}/media")
    suspend fun attachProductMedia(
        @Path("productId") productId: String,
        @Body body: AttachProductMediaRequest,
    ): Response<ApiEnvelope<ProductMediaListDto>>

    /** The per-asset attach. See [attachProductMedia] for why both exist. */
    @POST("v1/commerce/products/{productId}/media")
    suspend fun attachOneProductMedia(
        @Path("productId") productId: String,
        @Body body: AttachOneProductMediaRequest,
    ): Response<ApiEnvelope<ProductMediaListDto>>

    // ─── Cart ────────────────────────────────────────────────────────

    @GET("v1/commerce/cart")
    suspend fun getCart(): Response<ApiEnvelope<CartDto>>

    @POST("v1/commerce/cart/items")
    suspend fun addToCart(@Body body: AddToCartRequest): Response<ApiEnvelope<CartDto>>

    @PATCH("v1/commerce/cart/items/by-variant/{variantId}")
    suspend fun updateCartItem(
        @Path("variantId") variantId: String,
        @Body body: UpdateCartItemRequest,
    ): Response<ApiEnvelope<CartDto>>

    @DELETE("v1/commerce/cart/items/{variantId}")
    suspend fun removeCartItem(
        @Path("variantId") variantId: String,
    ): Response<ApiEnvelope<CartDto>>

    // ─── Address ─────────────────────────────────────────────────────

    @GET("v1/commerce/addresses")
    suspend fun listAddresses(): Response<ApiEnvelope<List<AddressDto>>>

    @POST("v1/commerce/addresses")
    suspend fun addAddress(@Body body: AddressDto): Response<ApiEnvelope<AddressDto>>

    // ─── Quote (A4) ──────────────────────────────────────────────────

    /**
     * Obtained BEFORE checkout, because it is a courier call and no network
     * call may happen inside the checkout transaction. The returned quote is
     * bound server-side to the cart version, address content, seller and item
     * set; changing any of them invalidates it and the client must re-quote.
     */
    @POST("v1/commerce/checkout/quote")
    suspend fun quote(@Body body: QuoteRequest): Response<ApiEnvelope<QuoteDto>>

    // ─── Checkout (LB-14, LB-15) ────────────────────────────────────

    @POST("v1/commerce/v2/orders/checkout")
    suspend fun checkout(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CheckoutRequest,
    ): Response<ApiEnvelope<CheckoutResultDto>>

    // ─── Payment (LB-4, A1) ──────────────────────────────────────────

    /**
     * Opens a payment for an order. Note the absence of an amount.
     *
     * Also the RETRY after a failed payment (contract §4.7, 2026-09-30): on an
     * order in `payment_failed` the server re-reserves the stock, moves the
     * order back to `payment_pending` and answers a fresh intent, or refuses
     * with 409 `OUT_OF_STOCK` when the stock has gone.
     */
    @POST("v1/commerce/orders/{orderId}/payment/intent")
    suspend fun openPayment(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<PaymentHandleDto>>

    /**
     * The authoritative payment state, three-state (contract §4.5,
     * 2026-09-30): `confirming | paid | failed` plus a `refund_status`. The
     * same shape as food-service's `GET /orders/:id/payment`.
     *
     * A server that predates the route answers a bare 404; the status source
     * then falls back ONCE, for that attempt, to [paymentStatus].
     */
    @GET("v1/commerce/orders/{orderId}/payment")
    suspend fun orderPayment(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<OrderPaymentDto>>

    /**
     * The older payment state, kept for the current build.
     *
     * A1: the app polls this after the PSP sheet returns. The redirect is
     * evidence, never proof — the order becomes paid only when a
     * signature-verified provider webhook reaches the server.
     */
    @GET("v1/commerce/orders/{orderId}/payment/status")
    suspend fun paymentStatus(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<PaymentStatusDto>>

    // ─── Orders ──────────────────────────────────────────────────────

    @GET("v1/commerce/orders")
    suspend fun listOrders(
        @Query("cursor") cursor: String? = null,
        @Query("limit") limit: Int = 20,
    ): Response<ApiEnvelope<OrderListDto>>

    @GET("v1/commerce/orders/{orderId}")
    suspend fun getOrder(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<OrderDto>>

    @POST("v1/commerce/orders/{orderId}/cancel")
    suspend fun cancelOrder(
        @Path("orderId") orderId: String,
        @Body body: CancelOrderRequest,
    ): Response<ApiEnvelope<Unit>>

    // ─── Seller ──────────────────────────────────────────────────────
    //
    // These are the seller half of the launch loop. They are separate from the
    // public catalogue routes on purpose: every one resolves the seller from
    // the CALLER, never from a path parameter, so a request body cannot name
    // somebody else's shop.

    /** The caller's own seller profile, including onboarding status. */
    @GET("v1/commerce/sellers/me")
    suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>>

    /**
     * The seller's OWN catalogue — every status, drafts and moderation
     * rejections included, because those are the ones they need to fix.
     *
     * Distinct from `GET /sellers/{id}/products`, which is the public
     * storefront and shows only what is on sale.
     */
    @GET("v1/commerce/seller/products")
    suspend fun sellerProducts(
        @Query("status") status: String? = null,
        @Query("limit") limit: Int = 50,
        @Query("offset") offset: Int = 0,
    ): Response<ApiEnvelope<SellerProductsDto>>

    @GET("v1/commerce/seller/variants/{variantId}/stock")
    suspend fun stock(
        @Path("variantId") variantId: String,
    ): Response<ApiEnvelope<StockDto>>

    /**
     * A signed DELTA, never a new total.
     *
     * A "set stock to N" call is a lost-update generator: the screen renders
     * 42, two units sell while the seller is typing, they submit 52 meaning
     * "I added ten", and the two sold units are restored to the shelf.
     */
    @PATCH("v1/commerce/seller/variants/{variantId}/stock")
    suspend fun adjustStock(
        @Path("variantId") variantId: String,
        @Body body: AdjustStockRequest,
    ): Response<ApiEnvelope<StockDto>>

    /**
     * Opens a shop.
     *
     * Idempotent on the server: a second call for a user who already has a
     * draft returns the existing one rather than creating a second shop.
     */
    @POST("v1/commerce/onboarding/start")
    suspend fun startSelling(
        @Body body: StartSellingRequest,
    ): Response<ApiEnvelope<SellerProfileDto>>

    /** The GST rate table. Public — statutory rates, not seller data. */
    @GET("v1/commerce/tax-classes")
    suspend fun taxClasses(): Response<ApiEnvelope<TaxClassListDto>>

    /**
     * Creates a product.
     *
     * Money is PAISE. The route still reads the legacy rupee fields when the
     * minor ones are absent, but a client written today has no reason to send
     * a float for the one number a seller types by hand.
     */
    @POST("v1/commerce/products")
    suspend fun createProduct(
        @Body body: CreateProductRequest,
    ): Response<ApiEnvelope<SellerProductDto>>

    /** One of the caller's own variants: price, availability, and status. */
    @GET("v1/commerce/seller/variants/{variantId}")
    suspend fun sellerVariant(
        @Path("variantId") variantId: String,
    ): Response<ApiEnvelope<SellerVariantDto>>

    /**
     * Changes a variant.
     *
     * Money is PAISE. Sending the rupee form as well is accepted only when the
     * two agree — the server refuses a body whose two shapes of the same
     * price describe different money, because picking one silently decides
     * what the buyer is charged.
     */
    @PATCH("v1/commerce/variants/{variantId}")
    suspend fun updateVariant(
        @Path("variantId") variantId: String,
        @Body body: UpdateVariantRequest,
    ): Response<ApiEnvelope<VariantDto>>

    /** Submits one product for moderation review. */
    @POST("v1/commerce/products/{productId}/submit")
    suspend fun submitProduct(
        @Path("productId") productId: String,
    ): Response<ApiEnvelope<Unit>>

    /** What the shop still needs before it can be reviewed. */
    @GET("v1/commerce/onboarding/readiness")
    suspend fun sellerReadiness(): Response<ApiEnvelope<ReadinessDto>>

    /**
     * The seller's KYC documents.
     *
     * Each `media_id` is verified server-side against media-service: it must
     * belong to the caller, be ready, and have passed moderation. A seller
     * cannot point their KYC at somebody else's uploaded identity document.
     */
    @PUT("v1/commerce/onboarding/step/documents")
    suspend fun saveDocuments(
        @Body body: SaveDocumentsRequest,
    ): Response<ApiEnvelope<Unit>>

    /** Where the seller is paid. */
    @PUT("v1/commerce/onboarding/step/payout")
    suspend fun savePayout(
        @Body body: PayoutRequest,
    ): Response<ApiEnvelope<Unit>>

    /** Submits the application for review. */
    @POST("v1/commerce/onboarding/submit")
    suspend fun submitSellerApplication(): Response<ApiEnvelope<Unit>>

    /** The pickup point — the origin of every shipment this seller sends. */
    @PUT("v1/commerce/seller/address")
    suspend fun saveSellerAddress(
        @Body body: SellerAddressRequest,
    ): Response<ApiEnvelope<Unit>>

    // Seller orders, fulfilment, returns and earnings.
    //
    // The DTOs live in SellerFulfilmentDtos.kt. One shape below is unusual
    // and worth knowing before reading it: the order list is a BARE array of
    // order rows paged by offset (commerce-service's ListMySellerOrders
    // writes the slice itself, and has no cursor).

    /**
     * The seller's orders, newest first.
     *
     * Offset-paged: `GET /v1/commerce/seller/orders` reads `limit` and
     * `offset` and answers a bare array, with no `next_cursor`. A full page
     * is the only signal that another may exist.
     */
    @GET("v1/commerce/seller/orders")
    suspend fun sellerOrders(
        @Query("limit") limit: Int = SELLER_PAGE_SIZE,
        @Query("offset") offset: Int = 0,
    ): Response<ApiEnvelope<List<SellerOrderDto>>>

    /** One order from the seller's side: their lines, their shipment, the buyer's address. */
    @GET("v1/commerce/seller/orders/{orderId}")
    suspend fun sellerOrder(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<SellerOrderCardDto>>

    /**
     * The fulfilment actions, under `/v1/commerce/seller/orders/{id}/...`
     * where the seller's other order reads live. Each maps to one row of the
     * D6 matrix the server enforces (migration 010,
     * `order_status_transitions`, actor `seller`); a move the matrix forbids
     * is a 409, and a repeat of a move already made is a 200 with
     * `applied: false`, which the repository treats as success.
     *
     * Pack and cancel answer a [SellerFulfilmentResultDto]. Ship is the
     * seller-prefixed spelling of `POST /orders/{id}/shipment`, so it answers
     * that route's `{"shipments": [...]}` at 201.
     */
    @POST("v1/commerce/seller/orders/{orderId}/pack")
    suspend fun packOrder(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<SellerFulfilmentResultDto>>

    @POST("v1/commerce/seller/orders/{orderId}/ship")
    suspend fun shipOrder(
        @Path("orderId") orderId: String,
        @Body body: ShipOrderRequest,
    ): Response<ApiEnvelope<ShipmentsDto>>

    @POST("v1/commerce/seller/orders/{orderId}/cancel")
    suspend fun sellerCancelOrder(
        @Path("orderId") orderId: String,
        @Body body: SellerCancelOrderRequest,
    ): Response<ApiEnvelope<SellerFulfilmentResultDto>>

    /**
     * The order's status history, oldest first: what the server actually
     * recorded, rather than a timeline the app would otherwise derive from
     * the order's timestamps. Same ownership rule as the detail read.
     */
    @GET("v1/commerce/seller/orders/{orderId}/history")
    suspend fun sellerOrderHistory(
        @Path("orderId") orderId: String,
    ): Response<ApiEnvelope<SellerOrderHistoryDto>>

    /**
     * The returns inbox. `status` narrows to requested, approved, rejected
     * or refunded; absent means everything.
     */
    @GET("v1/commerce/seller/returns")
    suspend fun sellerReturns(
        @Query("status") status: String? = null,
        @Query("limit") limit: Int = SELLER_PAGE_SIZE,
        @Query("offset") offset: Int = 0,
    ): Response<ApiEnvelope<SellerReturnsDto>>

    /**
     * Approve and reject are NOT under `/seller/`: they are
     * `/v1/commerce/returns/{id}/approve|reject`, and the server checks that
     * the caller is the seller of the returned item. Approving books the
     * reverse pickup and queues the refund; rejecting needs a reason the
     * buyer will read.
     */
    @POST("v1/commerce/returns/{returnId}/approve")
    suspend fun approveReturn(
        @Path("returnId") returnId: String,
    ): Response<ApiEnvelope<ReturnRequestDto>>

    @POST("v1/commerce/returns/{returnId}/reject")
    suspend fun rejectReturn(
        @Path("returnId") returnId: String,
        @Body body: RejectReturnRequest,
    ): Response<ApiEnvelope<ReturnRequestDto>>

    /**
     * Delivered prepaid lines with the gross, commission, fee, TDS and net
     * broken out. COD money is settled through a separate remittance ledger
     * and is not in this list. Offset-paged, default 50 on the server.
     */
    @GET("v1/commerce/seller/earnings")
    suspend fun sellerEarnings(
        @Query("limit") limit: Int = EARNINGS_PAGE_SIZE,
        @Query("offset") offset: Int = 0,
    ): Response<ApiEnvelope<SellerEarningsDto>>
}

/** The server's own default for its seller lists. */
const val SELLER_PAGE_SIZE = 20

/** The earnings route defaults to 50; asking for the same keeps one page one screen. */
const val EARNINGS_PAGE_SIZE = 50

// ─── Wire DTOs ───────────────────────────────────────────────────────
//
// Every money field is `_minor` and typed [Paise]. The server also emits
// deprecated rupee mirrors during the dual-write window; they are
// deliberately NOT declared here. A field that does not exist in the DTO
// cannot be read by mistake, which is a stronger guarantee than a comment
// telling the next person not to.
//
// Every RESPONSE field carries a default (house rule, 2026-09-30). A row the
// server sends in a shape this build does not expect — an id missing, a
// title absent — decodes to its zero value and is SKIPPED by the repository
// (`id` blank) rather than throwing and taking the whole list down with it.
// Request DTOs keep their required fields: the client knows what it sends.

@Serializable
data class ProductListDto(
    val items: List<ProductSummaryDto> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

@Serializable
data class ProductSummaryDto(
    val id: String = "",
    val title: String = "",
    @SerialName("brand_name") val brandName: String? = null,
    @SerialName("primary_image_media_id") val primaryImageMediaId: String? = null,
    // Resolved server-side. Commerce used to hand back only the media UUID,
    // and :core:commerce has no dependency on :core:media (where the resolver
    // lives), so no product screen could draw an image. Absent when
    // media-service is unreachable — the read path fails soft and the UI
    // shows a placeholder rather than the catalogue failing to load.
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    @SerialName("min_price_minor") val minPriceMinor: Paise = Paise.ZERO,
    @SerialName("mrp_minor") val mrpMinor: Paise = Paise.ZERO,
    @SerialName("avg_rating") val avgRating: Float = 0f,
    @SerialName("review_count") val reviewCount: Int = 0,
    @SerialName("in_stock") val inStock: Boolean = true,
    /**
     * The saving, as whole percent, computed by the server.
     *
     * Absent on a server that does not publish it yet; the domain then
     * derives it from mrp and price with
     * [com.us.android.core.commerce.model.discountPercent], which is the ONE
     * place the client is allowed to do that arithmetic.
     */
    @SerialName("discount_pct") val discountPct: Int? = null,
    /** Whether the CALLER has saved this product. Absent for an anonymous read. */
    @SerialName("is_favourite") val isFavourite: Boolean = false,
)

/** `POST /v1/commerce/favourites`: the product to save, by id. */
@Serializable
data class AddFavouriteRequest(
    @SerialName("product_id") val productId: String,
)

/**
 * The server's answer to a favourite write: the id it acted on and the
 * state it now holds. The repository does not need either today (the heart
 * is already optimistic), but decoding the real shape rather than `Unit`
 * means a future "server says it is NOT saved" can be honoured without a
 * wire change.
 */
@Serializable
data class FavouriteDto(
    @SerialName("product_id") val productId: String = "",
    @SerialName("is_favourite") val isFavourite: Boolean = false,
)

/**
 * One node of the shop's taxonomy.
 *
 * `image_url` is optional because the seeded taxonomy predates category
 * artwork; the strip draws a Lucide glyph when there is none rather than a
 * broken frame.
 *
 * `product_count` is the live number of published products in the subtree.
 * It was added so the strip can dim a category that would open onto nothing,
 * which is what made the category filter look broken when every seeded
 * category was empty. It defaults to 0 so a server that predates the field
 * still decodes; that server's tiles will read as empty, which is the honest
 * fallback for a count nobody sent.
 */
@Serializable
data class CategoryDto(
    val id: String = "",
    val name: String = "",
    val slug: String = "",
    @SerialName("parent_id") val parentId: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("display_order") val displayOrder: Int = 0,
    @SerialName("is_featured") val isFeatured: Boolean = false,
    @SerialName("product_count") val productCount: Int = 0,
)

/**
 * One node of `GET /categories?tree=true` (`postgres.CategoryTreeNode`).
 *
 * `is_listable` is whether a product may sit directly on this node; a
 * heading that only groups other categories is not listable. `children` is
 * always present on the wire (`[]` for a leaf).
 */
@Serializable
data class CategoryTreeDto(
    val id: String = "",
    @SerialName("parent_id") val parentId: String? = null,
    val name: String = "",
    val slug: String = "",
    val description: String? = null,
    @SerialName("display_order") val displayOrder: Int = 0,
    @SerialName("is_active") val isActive: Boolean = true,
    @SerialName("is_featured") val isFeatured: Boolean = false,
    @SerialName("is_listable") val isListable: Boolean = true,
    @SerialName("image_media_id") val imageMediaId: String? = null,
    @SerialName("product_count") val productCount: Int = 0,
    val depth: Int = 0,
    val children: List<CategoryTreeDto> = emptyList(),
)

/** The landing page: the banner rail, then the named shelves. */
@Serializable
data class HomeDto(
    val banners: List<HomeBannerDto> = emptyList(),
    val sections: List<HomeSectionDto> = emptyList(),
)

@Serializable
data class HomeBannerDto(
    val id: String = "",
    val title: String = "",
    val subtitle: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    /** "category", "product", "search" — anything else is not tappable. */
    @SerialName("target_type") val targetType: String? = null,
    @SerialName("target_id") val targetId: String? = null,
)

@Serializable
data class HomeSectionDto(
    /** Stable machine key: "deals", "best_sellers", "new_arrivals", ... */
    val key: String = "",
    val title: String = "",
    val products: List<ProductSummaryDto> = emptyList(),
)

/**
 * A product's gallery, as `GET` and `POST /products/:id/media` answer it.
 *
 * 2026-09-30: the server writes the list under `items` (handler.go,
 * `ListProductMedia` and `AttachProductMedia`, pinned by the
 * `storefront/product_media_200.json` golden). This DTO used to read only
 * `media`, so every gallery decoded empty and a seller's attached images
 * never came back to the screen. `media` stays declared for any build of the
 * server that wrote it; [gallery] is what the repository reads.
 */
@Serializable
data class ProductMediaListDto(
    val items: List<ProductMediaDto> = emptyList(),
    val media: List<ProductMediaDto> = emptyList(),
) {
    val gallery: List<ProductMediaDto> get() = items.ifEmpty { media }
}

@Serializable
data class ProductMediaDto(
    val id: String = "",
    @SerialName("media_id") val mediaId: String = "",
    @SerialName("media_type") val mediaType: String = "image",
    @SerialName("sort_order") val sortOrder: Int = 0,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
)

/** The gallery, in order. Index 0 is the cover. */
@Serializable
data class AttachProductMediaRequest(
    @SerialName("media_ids") val mediaIds: List<String>,
)

@Serializable
data class AttachOneProductMediaRequest(
    @SerialName("media_id") val mediaId: String,
    @SerialName("media_type") val mediaType: String = "image",
    @SerialName("sort_order") val sortOrder: Int = 0,
)

@Serializable
data class ProductDetailDto(
    val product: ProductBodyDto = ProductBodyDto(),
    val variants: List<VariantDto> = emptyList(),
)

@Serializable
data class ProductBodyDto(
    val id: String = "",
    val title: String = "",
    val description: String? = null,
    @SerialName("brand_name") val brandName: String? = null,
    @SerialName("primary_image_media_id") val primaryImageMediaId: String? = null,
    // Resolved server-side. Commerce used to hand back only the media UUID,
    // and :core:commerce has no dependency on :core:media (where the resolver
    // lives), so no product screen could draw an image. Absent when
    // media-service is unreachable — the read path fails soft and the UI
    // shows a placeholder rather than the catalogue failing to load.
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    @SerialName("seller_id") val sellerId: String = "",
    @SerialName("seller_name") val sellerName: String? = null,
    @SerialName("avg_rating") val avgRating: Float = 0f,
    @SerialName("review_count") val reviewCount: Int = 0,
    /**
     * Virtual try-on, when the seller has set it up. Absent on every server
     * that predates the field, which means exactly "not capable".
     *
     * Decoded through [TryOnDtoOrNull], so a `try_on` of ANY unexpected shape
     * — a null, an array, a number, an object with a wrong-typed field —
     * becomes null rather than failing the whole product read. A try-on is a
     * flourish; a product detail page that will not load is an outage.
     */
    @Serializable(with = TryOnDtoOrNull::class)
    @SerialName("try_on")
    val tryOn: TryOnDto? = null,
)

/**
 * `try_on` on a product.
 *
 * `capable` is stated by the server rather than inferred from the other fields
 * being filled in, because "not finished setting this up" and "switched off"
 * are different answers that must both mean no. It defaults to FALSE: an
 * object that arrives with fields this build does not understand and no
 * `capable` must not turn try-on on.
 */
@Serializable
data class TryOnDto(
    val capable: Boolean = false,
    /** "eyewear", "makeup", "jewellery", "watch". Anything else is not offered. */
    val kind: String = "",
    /** Joins the product to an effect bundle on disk, character for character. */
    @SerialName("effect_slug") val effectSlug: String = "",
    val variants: List<TryOnVariantDto> = emptyList(),
)

/**
 * One shade or colourway.
 *
 * `hex` is absent for a variant that is not a colour (a frame size).
 *
 * `js` is the contract's escape hatch: the exact effect-specific call for a
 * look a hex cannot drive — a gradient lens, a two-tone strap. Optional, and
 * absent on nearly every variant; when present it REPLACES the call the client
 * would have generated.
 */
@Serializable
data class TryOnVariantDto(
    val id: String = "",
    val label: String = "",
    val hex: String? = null,
    val js: String? = null,
)

/**
 * `try_on`, or null for anything that is not a decodable `try_on` object.
 *
 * WHY A CUSTOM SERIALIZER AND NOT JUST A NULLABLE FIELD
 *
 * A nullable field with a default already survives an ABSENT key and a JSON
 * null — `ignoreUnknownKeys` and `explicitNulls = false` cover those. What it
 * does not survive is a wrong SHAPE: `"try_on": []` or `"try_on": 3` or an
 * object whose `variants` is a string throws `SerializationException`, and
 * because this field sits on the product body that exception takes the entire
 * product detail page down. The server contract for `try_on` is being written
 * in parallel with this client, which is exactly when shapes change.
 *
 * So: read the raw element, attempt the decode, and treat any failure as "no
 * try-on". The product still loads; only the flourish is missing.
 */
internal object TryOnDtoOrNull : KSerializer<TryOnDto?> {

    // A descriptor of its own rather than TryOnDto's: this serializer decodes
    // a raw element by hand, so the descriptor is only ever used to decide
    // that the field is a structure and is not itself nullable — which is what
    // makes a JSON `null` resolve to null before deserialize is ever called.
    override val descriptor: SerialDescriptor =
        buildClassSerialDescriptor("commerce.TryOnDtoOrNull")

    override fun deserialize(decoder: Decoder): TryOnDto? {
        val json = decoder as? JsonDecoder ?: return null
        val element = runCatching { json.decodeJsonElement() }.getOrNull()
        if (element !is JsonObject) return null
        return runCatching { json.json.decodeFromJsonElement(TryOnDto.serializer(), element) }.getOrNull()
    }

    override fun serialize(encoder: Encoder, value: TryOnDto?) {
        // The client never SENDS try_on; this exists so the serializer is
        // total rather than half-implemented with a throw in it.
        val json = encoder as? JsonEncoder ?: return
        json.encodeJsonElement(
            value?.let { json.json.encodeToJsonElement(TryOnDto.serializer(), it) } ?: JsonNull,
        )
    }
}

@Serializable
data class VariantDto(
    val id: String = "",
    val sku: String = "",
    @SerialName("option_1_name") val option1Name: String? = null,
    @SerialName("option_1_value") val option1Value: String? = null,
    @SerialName("option_2_name") val option2Name: String? = null,
    @SerialName("option_2_value") val option2Value: String? = null,
    @SerialName("option_3_name") val option3Name: String? = null,
    @SerialName("option_3_value") val option3Value: String? = null,
    @SerialName("mrp_minor") val mrpMinor: Paise = Paise.ZERO,
    @SerialName("selling_price_minor") val sellingPriceMinor: Paise = Paise.ZERO,
    @SerialName("available_qty") val availableQty: Int = 0,
    val status: String = "active",
)

/** `GET /products/:id/variants`: `{items: [...]}`, every status, creation order. */
@Serializable
data class VariantListDto(val items: List<VariantDto> = emptyList())

@Serializable
data class CartDto(
    @SerialName("cart_id") val cartId: String? = null,
    val items: List<CartLineDto> = emptyList(),
    @SerialName("subtotal_minor") val subtotalMinor: Paise = Paise.ZERO,
    @SerialName("item_count") val itemCount: Int = 0,
    @SerialName("seller_id") val sellerId: String? = null,
    @SerialName("seller_name") val sellerName: String? = null,
)

@Serializable
data class CartLineDto(
    @SerialName("variant_id") val variantId: String = "",
    @SerialName("product_id") val productId: String = "",
    val title: String = "",
    @SerialName("image_media_id") val imageMediaId: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    val quantity: Int = 1,
    @SerialName("unit_price_minor") val unitPriceMinor: Paise = Paise.ZERO,
    @SerialName("line_total_minor") val lineTotalMinor: Paise = Paise.ZERO,
    /** Present when the catalogue price moved since the line was added. */
    @SerialName("price_was_minor") val priceWasMinor: Paise? = null,
    @SerialName("available_qty") val availableQty: Int? = null,
    /** False when the product left the catalogue after the line was added. */
    val sellable: Boolean = true,
)

@Serializable
data class AddToCartRequest(
    @SerialName("variant_id") val variantId: String,
    val quantity: Int,
)

@Serializable
data class UpdateCartItemRequest(val quantity: Int)

/**
 * An address, read AND written.
 *
 * The defaults are for the read: a row the server sends without a name is
 * skipped by the repository rather than failing the whole address book. On
 * the write the app fills every field before sending, so the defaults never
 * shape a request (and with `encodeDefaults` off, an empty field is simply
 * absent, which the server refuses the same way it refuses an empty one).
 */
@Serializable
data class AddressDto(
    val id: String = "",
    val label: String = "Home",
    @SerialName("contact_name") val contactName: String = "",
    val phone: String = "",
    @SerialName("address_line_1") val line1: String = "",
    @SerialName("address_line_2") val line2: String? = null,
    val landmark: String? = null,
    val city: String = "",
    val state: String = "",
    @SerialName("postal_code") val postalCode: String = "",
    @SerialName("is_default") val isDefault: Boolean = false,
)

@Serializable
data class QuoteRequest(
    @SerialName("address_id") val addressId: String,
    /**
     * C3-LB-2: both travel with the quote because both change the price, and
     * the server binds the quote to them. Sending a different coupon or
     * method at checkout than the quote was priced under is refused rather
     * than silently repriced.
     */
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("payment_method") val paymentMethod: String? = null,
)

@Serializable
data class QuoteDto(
    @SerialName("quote_id") val quoteId: String = "",
    // C3-LB-2 — the whole breakdown, server-computed. `total_minor` is the
    // number the buyer approves and the number sent back as
    // `expected_total_minor`; `tax_minor` is the GST already INSIDE it (D1),
    // published for display and never to be added on.
    @SerialName("subtotal_minor") val subtotalMinor: Paise = Paise.ZERO,
    @SerialName("discount_minor") val discountMinor: Paise = Paise.ZERO,
    @SerialName("shipping_minor") val shippingMinor: Paise = Paise.ZERO,
    @SerialName("tax_minor") val taxMinor: Paise = Paise.ZERO,
    @SerialName("total_minor") val totalMinor: Paise = Paise.ZERO,
    val currency: String = "INR",
    @SerialName("courier_code") val courierCode: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    val serviceable: Boolean = true,
    val reason: String? = null,
)

@Serializable
data class CheckoutRequest(
    @SerialName("address_id") val addressId: String,
    @SerialName("quote_id") val quoteId: String,
    @SerialName("payment_method") val paymentMethod: String,
    @SerialName("coupon_code") val couponCode: String? = null,
    @SerialName("terms_version") val termsVersion: String? = null,
    /**
     * What the customer was last shown. A mismatch returns a typed
     * PRICE_CHANGED response rather than silently charging a different
     * number — which is the only reason the client sends a total at all. It
     * is a claim to be checked, never an instruction.
     */
    @SerialName("expected_total_minor") val expectedTotalMinor: Long = 0,
)

@Serializable
data class CheckoutResultDto(
    @SerialName("order_id") val orderId: String = "",
    @SerialName("order_number") val orderNumber: String = "",
    @SerialName("total_minor") val totalMinor: Paise = Paise.ZERO,
    @SerialName("tax_minor") val taxMinor: Paise = Paise.ZERO,
    @SerialName("shipping_minor") val shippingMinor: Paise = Paise.ZERO,
    val currency: String = "INR",
    @SerialName("payment_intent_id") val paymentIntentId: String? = null,
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
)

@Serializable
data class PaymentHandleDto(
    @SerialName("payment_intent_id") val paymentIntentId: String = "",
    @SerialName("amount_minor") val amountMinor: Paise = Paise.ZERO,
    val currency: String = "INR",
    @SerialName("provider_ref") val providerRef: String? = null,
    val status: String = "pending",
    /**
     * What the PSP's client SDK needs to open checkout: the provider name,
     * its order handle, and the PUBLISHABLE key.
     *
     * It comes from the server that created the provider order, deliberately.
     * A key compiled into the app can disagree with the server's environment
     * — a test-key build cannot open a sheet for a live-key order — and
     * sourcing it here makes that disagreement impossible rather than merely
     * unlikely.
     *
     * Absent when the provider cannot derive a session from the order id
     * alone. The app then says it cannot open a sheet rather than opening one
     * that will fail.
     */
    @SerialName("client_session") val clientSession: Map<String, String>? = null,
)

/**
 * `GET /orders/:id/payment` (contract §4.5): the three-state payment read.
 *
 * `status` is `confirming | paid | failed`; `refund_status` is null or
 * `pending | partially_refunded | refunded`. The same names as food-service's
 * `OrderPaymentDto`, and pinned by the same `order_payment_get_200_*.json`
 * fixture shape.
 */
@Serializable
data class OrderPaymentDto(
    @SerialName("order_id") val orderId: String = "",
    val status: String = "",
    @SerialName("amount_minor") val amountMinor: Paise = Paise.ZERO,
    val currency: String = "INR",
    @SerialName("refund_status") val refundStatus: String? = null,
    @SerialName("updated_at") val updatedAt: String? = null,
)

@Serializable
data class PaymentStatusDto(
    @SerialName("order_id") val orderId: String = "",
    @SerialName("order_status") val orderStatus: String = "",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("provider_status") val providerStatus: String? = null,
)

@Serializable
data class OrderListDto(
    val items: List<OrderDto> = emptyList(),
    @SerialName("next_cursor") val nextCursor: String? = null,
)

@Serializable
data class OrderDto(
    val id: String = "",
    @SerialName("order_number") val orderNumber: String = "",
    val status: String = "",
    @SerialName("payment_status") val paymentStatus: String = "",
    @SerialName("created_at_epoch") val createdAtEpoch: Long = 0,
    @SerialName("subtotal_minor") val subtotalMinor: Paise = Paise.ZERO,
    @SerialName("discount_minor") val discountMinor: Paise = Paise.ZERO,
    @SerialName("shipping_minor") val shippingMinor: Paise = Paise.ZERO,
    @SerialName("tax_minor") val taxMinor: Paise = Paise.ZERO,
    @SerialName("total_minor") val totalMinor: Paise = Paise.ZERO,
    val currency: String = "INR",
    val items: List<OrderLineDto> = emptyList(),
    /** The immutable snapshot stored on the order (LB-18). */
    @SerialName("delivery_address") val deliveryAddress: AddressDto? = null,
    @SerialName("can_cancel") val canCancel: Boolean = false,
    /**
     * Whether `POST /orders/:id/payment/intent` would re-open this order
     * (contract §4.7). Absent on a server that predates the retry; the
     * screen then offers "Pay now" on `payment_failed` regardless and lets
     * the intent route answer.
     */
    @SerialName("can_retry_payment") val canRetryPayment: Boolean? = null,
    @SerialName("tracking_url") val trackingUrl: String? = null,
    /**
     * The LIST row's summary (2026-09-30, `orders/orders_list_200.json`):
     * `GET /orders` sends no `items`, only how many lines the order has and
     * the first line's title. Without these a list row had nothing to say
     * about what was bought. Zero and null on the detail read, which sends
     * the lines themselves.
     */
    @SerialName("item_count") val itemCount: Int = 0,
    @SerialName("first_product_title") val firstProductTitle: String? = null,
)

@Serializable
data class OrderLineDto(
    @SerialName("product_id") val productId: String = "",
    @SerialName("variant_id") val variantId: String = "",
    @SerialName("product_title") val title: String = "",
    @SerialName("image_media_id") val imageMediaId: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    val quantity: Int = 1,
    @SerialName("unit_price_minor") val unitPriceMinor: Paise = Paise.ZERO,
    /**
     * The line's total as the buyer's order read sends it (2026-09-30,
     * `orders/order_get_200_*.json`). The DTO used to read only
     * `final_price_minor`, which that route does not send, so every line on
     * the order detail read ₹0. [finalPriceMinor] stays for a server that
     * wrote it.
     */
    @SerialName("line_total_minor") val lineTotalMinor: Paise = Paise.ZERO,
    @SerialName("final_price_minor") val finalPriceMinor: Paise = Paise.ZERO,
) {
    /** The line total: `line_total_minor` when sent, else `final_price_minor` (Go sends 0 for "none"). */
    val total: Paise get() = if (lineTotalMinor != Paise.ZERO) lineTotalMinor else finalPriceMinor
}

@Serializable
data class CancelOrderRequest(val reason: String)

// ─── Seller DTOs ─────────────────────────────────────────────────────

/**
 * The caller's own seller profile.
 *
 * `status` is the onboarding/approval state machine — draft, submitted,
 * under_review, changes_required, approved, rejected, suspended, disabled. It
 * is NOT `verification_status`, which is the separate KYC column: a seller can
 * be format-checked and still not approved to sell.
 */
@Serializable
data class SellerProfileDto(
    val id: String = "",
    @SerialName("store_name") val storeName: String = "",
    val status: String = "",
    @SerialName("onboarding_step") val onboardingStep: Int = 0,
    val state: String? = null,
    val city: String? = null,
    @SerialName("postal_code") val postalCode: String? = null,
    @SerialName("total_products") val totalProducts: Int = 0,
    @SerialName("total_orders") val totalOrders: Int = 0,
)

@Serializable
data class SellerProductsDto(
    val items: List<SellerProductDto> = emptyList(),
    val total: Int = 0,
)

/**
 * A row of the seller's own catalogue.
 *
 * Carries both `status` and `approval_status` because they answer different
 * questions and a seller needs both: `status` is whether the seller has it
 * switched on, `approval_status` is whether moderation has let it through.
 *
 * `variants` (contract §6, lane C1) is the product's variants with their own
 * ids, so a stock or price edit can be addressed without a second read. A
 * server that predates it sends none, and the repository then reads
 * `GET /products/:id/variants`. `default_variant_id` is the list projection's
 * shorthand for the same thing when it is present.
 */
@Serializable
data class SellerProductDto(
    val id: String = "",
    val title: String = "",
    val status: String = "",
    @SerialName("approval_status") val approvalStatus: String = "",
    @SerialName("rejection_reason") val rejectionReason: String? = null,
    @SerialName("image_url") val imageUrl: String? = null,
    @SerialName("thumbnail_url") val thumbnailUrl: String? = null,
    val variants: List<VariantDto> = emptyList(),
    @SerialName("default_variant_id") val defaultVariantId: String? = null,
)

/**
 * A variant's stock level.
 *
 * `reserved` is shown separately from `total` because a seller looking at "42"
 * needs to know how many are already promised to orders being placed right now
 * before deciding what they can physically ship today.
 */
@Serializable
data class StockDto(
    @SerialName("variant_id") val variantId: String = "",
    @SerialName("total_qty") val totalQty: Int = 0,
    @SerialName("reserved_qty") val reservedQty: Int = 0,
    val available: Int = 0,
)

@Serializable
data class AdjustStockRequest(
    /** Positive to restock, negative to write down. Never an absolute total. */
    val delta: Int,
    val reason: String,
    val notes: String? = null,
)

@Serializable
data class SellerAddressRequest(
    @SerialName("address_type") val addressType: String = "pickup",
    @SerialName("contact_name") val contactName: String,
    val phone: String,
    @SerialName("address_line_1") val addressLine1: String,
    @SerialName("address_line_2") val addressLine2: String? = null,
    val city: String,
    /** Half of the GST place-of-supply comparison. Required, not optional. */
    val state: String,
    /** The courier's origin. Required, not optional. */
    @SerialName("postal_code") val postalCode: String,
    val country: String = "IN",
    @SerialName("is_default") val isDefault: Boolean = true,
)

@Serializable
data class StartSellingRequest(
    @SerialName("store_name") val storeName: String,
    val email: String,
    /**
     * `individual` or `business`. Defaulted here rather than asked, because
     * the distinction changes which KYC documents are required and that is a
     * later step in the wizard — asking it on the first screen would make the
     * shortest path into selling the one that needs the most explanation.
     */
    @SerialName("seller_type") val sellerType: String = "individual",
)

@Serializable
data class TaxClassListDto(val items: List<TaxClassDto> = emptyList())

@Serializable
data class TaxClassDto(
    val id: String = "",
    val name: String = "",
    @SerialName("rate_percent") val ratePercent: Double = 0.0,
)

/**
 * Creating a product.
 *
 * `tax_class_id` is required, not optional. A product without a GST class is
 * not untaxed — it is unsellable: checkout resolves the rate under a row lock
 * and refuses with PRODUCT_TAX_UNCONFIGURED. The server rejects a create with
 * no class rather than letting a seller list something no buyer can complete.
 *
 * `category_id` (2026-09-30) is where the listing sits in browse. The server
 * accepts a create without one, and that product then appears in no
 * category, so the app refuses the create client-side until one is chosen.
 * Nullable with a null default so an older caller's body is unchanged.
 */
@Serializable
data class CreateProductRequest(
    val title: String,
    @SerialName("tax_class_id") val taxClassId: String,
    @SerialName("category_id") val categoryId: String? = null,
    val description: String? = null,
    @SerialName("primary_image_media_id") val primaryImageMediaId: String? = null,
    @SerialName("weight_grams") val weightGrams: Int? = null,
    val variants: List<CreateVariantRequest>,
)

@Serializable
data class CreateVariantRequest(
    val sku: String,
    /** Struck-through price, in paise. */
    @SerialName("mrp_minor") val mrpMinor: Paise,
    /** What the buyer pays, in paise. GST-inclusive, like every price here. */
    @SerialName("selling_price_minor") val sellingPriceMinor: Paise,
    @SerialName("stock_qty") val stockQty: Int = 0,
)

/**
 * A variant edit.
 *
 * Every field is nullable and omitted when null, so a partial edit sends only
 * what changed. Money is paise; the rupee form is never sent.
 */
@Serializable
data class UpdateVariantRequest(
    @SerialName("selling_price_minor") val sellingPriceMinor: Paise? = null,
    @SerialName("mrp_minor") val mrpMinor: Paise? = null,
    val status: String? = null,
)

/**
 * A variant as its seller sees it.
 *
 * Prices come from the same COALESCE the pricing path uses, so an edit screen
 * shows what a buyer would actually be charged rather than the NUMERIC mirror
 * — which is the column the original repricing defect left stale.
 */
@Serializable
data class SellerVariantDto(
    @SerialName("variant_id") val variantId: String = "",
    @SerialName("product_id") val productId: String = "",
    val title: String = "",
    val sku: String = "",
    @SerialName("selling_price_minor") val sellingPriceMinor: Paise = Paise.ZERO,
    @SerialName("mrp_minor") val mrpMinor: Paise = Paise.ZERO,
    val status: String = "active",
    @SerialName("total_qty") val totalQty: Int = 0,
    @SerialName("reserved_qty") val reservedQty: Int = 0,
    val available: Int = 0,
)

/**
 * What a shop still needs before it can be reviewed.
 *
 * `missing` names every outstanding item at once rather than the first, so the
 * app can show the whole checklist. `POST /onboarding/submit` enforces exactly
 * these rules — this is the friendly half, not the guard.
 */
@Serializable
data class ReadinessDto(
    val ready: Boolean = false,
    val missing: List<String> = emptyList(),
)

@Serializable
data class PayoutRequest(
    @SerialName("account_holder_name") val accountHolderName: String,
    @SerialName("account_number") val accountNumber: String,
    @SerialName("bank_name") val bankName: String? = null,
    @SerialName("ifsc_code") val ifscCode: String? = null,
    @SerialName("upi_id") val upiId: String? = null,
)

@Serializable
data class SaveDocumentsRequest(val documents: List<DocumentInput>)

@Serializable
data class DocumentInput(
    @SerialName("document_type") val documentType: String,
    @SerialName("media_id") val mediaId: String,
    @SerialName("document_number") val documentNumber: String? = null,
)
