package com.us.android.core.commerce.repository

import com.us.android.core.commerce.model.Address
import com.us.android.core.commerce.model.Cart
import com.us.android.core.commerce.model.CartLine
import com.us.android.core.commerce.model.DeliveryQuote
import com.us.android.core.commerce.model.NewProduct
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderLine
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PaymentHandle
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.PayoutAccount
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.commerce.model.Product
import com.us.android.core.commerce.model.ProductPage
import com.us.android.core.commerce.model.ProductSummary
import com.us.android.core.commerce.model.SellerAddress
import com.us.android.core.commerce.model.SellerDocument
import com.us.android.core.commerce.model.SellerProduct
import com.us.android.core.commerce.model.SellerProfile
import com.us.android.core.commerce.model.SellerReadiness
import com.us.android.core.commerce.model.SellerRequirement
import com.us.android.core.commerce.model.SellerStatus
import com.us.android.core.commerce.model.SellerVariant
import com.us.android.core.commerce.model.StockLevel
import com.us.android.core.commerce.model.StockReason
import com.us.android.core.commerce.model.TaxClass
import com.us.android.core.commerce.model.Variant
import com.us.android.core.commerce.model.VariantOption
import com.us.android.core.commerce.network.AddToCartRequest
import com.us.android.core.commerce.network.AddressDto
import com.us.android.core.commerce.network.AdjustStockRequest
import com.us.android.core.commerce.network.CancelOrderRequest
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CheckoutRequest
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.CreateVariantRequest
import com.us.android.core.commerce.network.DocumentInput
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.PayoutRequest
import com.us.android.core.commerce.network.QuoteRequest
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.network.SellerAddressRequest
import com.us.android.core.commerce.network.StartSellingRequest
import com.us.android.core.commerce.network.StockDto
import com.us.android.core.commerce.network.UpdateCartItemRequest
import com.us.android.core.commerce.network.UpdateVariantRequest
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import retrofit2.Response
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The commerce data layer.
 *
 * Two responsibilities that are easy to get wrong and expensive when wrong:
 *
 *  1. Mapping the server's stable error codes onto [CommerceError], so the
 *     UI can render §7's failure states instead of one generic banner.
 *  2. Owning the checkout idempotency key. It is minted here and REUSED
 *     across retries of the same attempt (LB-15) — a key regenerated per
 *     call would make every retry a new order, which is the duplicate-order
 *     blocker wearing a client-side hat.
 */
/*
 * TooManyFunctions: this is one API surface, not several responsibilities.
 *
 * Commerce is genuinely wide — catalogue, cart, addresses, quote, checkout,
 * payment, orders and the seller half — and every method here is the same
 * shape: one endpoint, mapped to one domain type. Splitting it to satisfy a
 * count would produce several repositories sharing the `call` helper, the
 * error mapping and the idempotency key, and every screen would then have to
 * know which of them to inject. The threshold is guarding against a class
 * doing several things; this one does one thing many times.
 */
@Suppress("TooManyFunctions")
@Singleton
class CommerceRepository @Inject constructor(
    private val api: CommerceApi,
) {

    // ─── Catalog ─────────────────────────────────────────────────────

    /**
     * One page of the catalogue.
     *
     * [cursor] is the server's opaque continuation token, echoed back
     * verbatim. The client must not synthesise or offset it: the server
     * pages on a keyset, and an invented cursor silently returns the wrong
     * window rather than failing.
     */
    suspend fun products(
        query: String? = null,
        categoryId: String? = null,
        cursor: String? = null,
        limit: Int = PAGE_SIZE,
    ): CommerceResult<ProductPage> =
        call { api.listProducts(query?.takeIf { it.isNotBlank() }, categoryId, cursor, limit) }
            .map { dto ->
                ProductPage(
                    items = dto.items.map { p ->
                        ProductSummary(
                            id = p.id,
                            title = p.title,
                            brandName = p.brandName,
                            primaryImageMediaId = p.primaryImageMediaId,
                            imageUrl = p.imageUrl,
                            thumbnailUrl = p.thumbnailUrl,
                            fromPrice = p.minPriceMinor,
                            mrp = p.mrpMinor,
                            avgRating = p.avgRating,
                            reviewCount = p.reviewCount,
                            inStock = p.inStock,
                        )
                    },
                    nextCursor = dto.nextCursor?.takeIf { it.isNotBlank() },
                )
            }

    suspend fun product(productId: String): CommerceResult<Product> =
        call { api.getProduct(productId) }.map { dto ->
            Product(
                id = dto.product.id,
                title = dto.product.title,
                description = dto.product.description,
                brandName = dto.product.brandName,
                primaryImageMediaId = dto.product.primaryImageMediaId,
                imageUrl = dto.product.imageUrl,
                thumbnailUrl = dto.product.thumbnailUrl,
                sellerId = dto.product.sellerId,
                sellerName = dto.product.sellerName,
                avgRating = dto.product.avgRating,
                reviewCount = dto.product.reviewCount,
                variants = dto.variants.map { v ->
                    Variant(
                        id = v.id,
                        sku = v.sku,
                        options = buildList {
                            if (!v.option1Name.isNullOrBlank() && !v.option1Value.isNullOrBlank()) {
                                add(VariantOption(v.option1Name, v.option1Value))
                            }
                            if (!v.option2Name.isNullOrBlank() && !v.option2Value.isNullOrBlank()) {
                                add(VariantOption(v.option2Name, v.option2Value))
                            }
                            if (!v.option3Name.isNullOrBlank() && !v.option3Value.isNullOrBlank()) {
                                add(VariantOption(v.option3Name, v.option3Value))
                            }
                        },
                        mrp = v.mrpMinor,
                        sellingPrice = v.sellingPriceMinor,
                        // Availability comes from the server. The client
                        // must not infer "in stock" from a price being
                        // present, which an earlier revision of this screen
                        // did and which showed sold-out items as buyable.
                        inStock = v.status == "active" && v.availableQty > 0,
                        availableQty = v.availableQty,
                    )
                },
            )
        }

    // ─── Cart ────────────────────────────────────────────────────────

    suspend fun cart(): CommerceResult<Cart> = call { api.getCart() }.map(::toCart)

    suspend fun addToCart(variantId: String, quantity: Int): CommerceResult<Cart> =
        call { api.addToCart(AddToCartRequest(variantId, quantity)) }.map(::toCart)

    suspend fun setQuantity(variantId: String, quantity: Int): CommerceResult<Cart> =
        call { api.updateCartItem(variantId, UpdateCartItemRequest(quantity)) }.map(::toCart)

    suspend fun removeFromCart(variantId: String): CommerceResult<Cart> =
        call { api.removeCartItem(variantId) }.map(::toCart)

    // ─── Address ─────────────────────────────────────────────────────

    suspend fun addresses(): CommerceResult<List<Address>> =
        call { api.listAddresses() }.map { list -> list.map(::toAddress) }

    /**
     * Saves a new delivery address.
     *
     * The `id` is server-assigned, so it is sent empty. Note that saving an
     * address does NOT alter any existing order: LB-18 made the order carry
     * an immutable snapshot, because editing a saved address used to rewrite
     * the delivery record of every past order and its GST invoice.
     */
    suspend fun addAddress(address: Address): CommerceResult<Address> =
        call {
            api.addAddress(
                AddressDto(
                    id = "",
                    label = address.label,
                    contactName = address.contactName,
                    phone = address.phone,
                    line1 = address.line1,
                    line2 = address.line2,
                    landmark = address.landmark,
                    city = address.city,
                    state = address.state,
                    postalCode = address.postalCode,
                    isDefault = address.isDefault,
                )
            )
        }.map(::toAddress)

    // ─── Quote ───────────────────────────────────────────────────────

    /**
     * Asks the server to price the current cart.
     *
     * C3-LB-2. This returns the COMPLETE breakdown the buyer will be asked to
     * approve, not just a delivery charge. The coupon and payment method
     * travel with the request because both change the price, and the server
     * binds the quote to them.
     *
     * Nothing here computes money. The five figures are relayed exactly as
     * the server stated them.
     */
    suspend fun quote(
        addressId: String,
        couponCode: String? = null,
        paymentMethod: String? = null,
    ): CommerceResult<DeliveryQuote> =
        call { api.quote(QuoteRequest(addressId, couponCode, paymentMethod)) }.let { result ->
            when (result) {
                is CommerceResult.Failure -> result
                is CommerceResult.Success -> {
                    val dto = result.value
                    if (!dto.serviceable) {
                        CommerceResult.Failure(CommerceError.NotServiceable(dto.reason))
                    } else {
                        CommerceResult.Success(
                            DeliveryQuote(
                                quoteId = dto.quoteId,
                                breakdown = PriceBreakdown(
                                    subtotal = dto.subtotalMinor,
                                    discount = dto.discountMinor,
                                    shipping = dto.shippingMinor,
                                    tax = dto.taxMinor,
                                    total = dto.totalMinor,
                                    currency = dto.currency,
                                ),
                                courierCode = dto.courierCode,
                                expiresAtEpochSeconds = 0L,
                            )
                        )
                    }
                }
            }
        }

    // ─── Checkout ────────────────────────────────────────────────────

    /**
     * Mints a checkout attempt key.
     *
     * LB-15: the SAME key must be sent for every retry of one customer
     * intent. A new key per HTTP call would defeat the server's unique index
     * entirely — which is exactly what the old server-side fallback
     * (`userID-<nanotime>`) did.
     */
    fun newCheckoutKey(): String = UUID.randomUUID().toString()

    suspend fun checkout(
        idempotencyKey: String,
        addressId: String,
        quoteId: String,
        paymentMethod: String,
        expectedTotal: Paise,
        couponCode: String? = null,
        termsVersion: String? = null,
    ): CommerceResult<CheckoutOutcome> =
        call {
            api.checkout(
                idempotencyKey,
                CheckoutRequest(
                    addressId = addressId,
                    quoteId = quoteId,
                    paymentMethod = paymentMethod,
                    couponCode = couponCode,
                    termsVersion = termsVersion,
                    expectedTotalMinor = expectedTotal.value,
                ),
            )
        }.map { dto ->
            CheckoutOutcome(
                orderId = dto.orderId,
                orderNumber = dto.orderNumber,
                breakdown = PriceBreakdown(
                    subtotal = dto.totalMinor - dto.shippingMinor,
                    discount = Paise.ZERO,
                    shipping = dto.shippingMinor,
                    tax = dto.taxMinor,
                    total = dto.totalMinor,
                    currency = dto.currency,
                ),
                paymentIntentId = dto.paymentIntentId,
                clientSession = dto.clientSession.orEmpty(),
            )
        }

    // ─── Payment ─────────────────────────────────────────────────────

    suspend fun openPayment(orderId: String): CommerceResult<PaymentHandle> =
        call { api.openPayment(orderId) }.map { dto ->
            PaymentHandle(
                paymentIntentId = dto.paymentIntentId,
                amount = dto.amountMinor,
                currency = dto.currency,
                providerRef = dto.providerRef,
                status = PaymentStatus.from(dto.status),
                clientSession = dto.clientSession.orEmpty(),
            )
        }

    /**
     * The authoritative payment state.
     *
     * A1: the PSP redirect is never proof. The screen polls this until the
     * server — which only marks an order paid on a signature-verified
     * provider webhook — says otherwise.
     */
    suspend fun paymentStatus(orderId: String): CommerceResult<PaymentStatus> =
        call { api.paymentStatus(orderId) }.map { PaymentStatus.from(it.paymentStatus) }

    // ─── Orders ──────────────────────────────────────────────────────

    suspend fun orders(cursor: String? = null): CommerceResult<List<Order>> =
        call { api.listOrders(cursor) }.map { it.items.map(::toOrder) }

    suspend fun order(orderId: String): CommerceResult<Order> =
        call { api.getOrder(orderId) }.map(::toOrder)

    suspend fun cancelOrder(orderId: String, reason: String): CommerceResult<Unit> =
        call { api.cancelOrder(orderId, CancelOrderRequest(reason)) }.map { }

    // ─── Seller ──────────────────────────────────────────────────────
    //
    // Every one of these resolves the seller from the CALLER on the server
    // side. Nothing here sends a seller id, and nothing should: a request body
    // that could name a shop is a request body that could name someone else's.

    suspend fun sellerProfile(): CommerceResult<SellerProfile> =
        call { api.sellerProfile() }.map { dto ->
            SellerProfile(
                id = dto.id,
                storeName = dto.storeName,
                status = SellerStatus.from(dto.status),
                onboardingStep = dto.onboardingStep,
                state = dto.state,
                city = dto.city,
                postalCode = dto.postalCode,
                totalProducts = dto.totalProducts,
                totalOrders = dto.totalOrders,
            )
        }

    /**
     * The seller's own catalogue, every status included.
     *
     * Deliberately not the public storefront endpoint: that one is filtered to
     * what is on sale, and a seller looking at their dashboard needs to see
     * exactly the rows that are NOT — the drafts they have not released and
     * the ones moderation turned down.
     */
    suspend fun sellerProducts(status: String? = null): CommerceResult<List<SellerProduct>> =
        call { api.sellerProducts(status?.takeIf { it.isNotBlank() }) }.map { dto ->
            dto.items.map { p ->
                SellerProduct(
                    id = p.id,
                    title = p.title,
                    status = p.status,
                    approvalStatus = p.approvalStatus,
                    rejectionReason = p.rejectionReason,
                    imageUrl = p.thumbnailUrl ?: p.imageUrl,
                )
            }
        }

    suspend fun stock(variantId: String): CommerceResult<StockLevel> =
        call { api.stock(variantId) }.map(::toStock)

    /**
     * Applies a signed delta.
     *
     * The caller passes how many units were added or removed — never a new
     * total. The current figure is the server's business, read under a row
     * lock, precisely so two people adjusting at once cannot lose one
     * another's write.
     */
    suspend fun adjustStock(
        variantId: String,
        delta: Int,
        reason: StockReason,
        notes: String? = null,
    ): CommerceResult<StockLevel> =
        call {
            api.adjustStock(
                variantId,
                AdjustStockRequest(
                    delta = delta,
                    reason = reason.wire,
                    notes = notes?.trim()?.takeIf { it.isNotBlank() },
                ),
            )
        }.map(::toStock)

    /**
     * Saves the pickup point.
     *
     * State and PIN are required by the server and both decide money: the PIN
     * is the courier's origin, and the state is the seller half of the GST
     * place-of-supply comparison.
     */
    suspend fun saveSellerAddress(address: SellerAddress): CommerceResult<Unit> =
        call {
            api.saveSellerAddress(
                SellerAddressRequest(
                    contactName = address.contactName.trim(),
                    phone = address.phone.trim(),
                    addressLine1 = address.line1.trim(),
                    addressLine2 = address.line2?.trim()?.takeIf { it.isNotBlank() },
                    city = address.city.trim(),
                    state = address.state.trim(),
                    postalCode = address.postalCode.trim(),
                ),
            )
        }.map { }

    /**
     * Opens a shop.
     *
     * Idempotent on the server, which matters here: a double-tap on the button
     * must not create a second shop, and the second call returns the first
     * one rather than failing.
     */
    suspend fun startSelling(storeName: String, email: String): CommerceResult<SellerProfile> =
        call {
            api.startSelling(
                StartSellingRequest(storeName = storeName.trim(), email = email.trim()),
            )
        }.map { dto ->
            SellerProfile(
                id = dto.id,
                storeName = dto.storeName,
                status = SellerStatus.from(dto.status),
                onboardingStep = dto.onboardingStep,
                state = dto.state,
                city = dto.city,
                postalCode = dto.postalCode,
                totalProducts = dto.totalProducts,
                totalOrders = dto.totalOrders,
            )
        }

    /** Submits the application for review. */
    suspend fun submitSellerApplication(): CommerceResult<Unit> =
        call { api.submitSellerApplication() }.map { }

    /** The GST rate table a seller chooses from when listing a product. */
    suspend fun taxClasses(): CommerceResult<List<TaxClass>> =
        call { api.taxClasses() }.map { dto ->
            dto.items.map { TaxClass(id = it.id, name = it.name, ratePercent = it.ratePercent) }
        }

    /**
     * Lists a product.
     *
     * The SKU is generated here when the seller does not supply one. It is an
     * internal identifier the buyer never sees, and demanding one on the first
     * listing is a question most sellers cannot answer.
     */
    suspend fun createProduct(product: NewProduct): CommerceResult<SellerProduct> =
        call {
            api.createProduct(
                CreateProductRequest(
                    title = product.title.trim(),
                    taxClassId = product.taxClassId,
                    description = product.description?.trim()?.takeIf { it.isNotBlank() },
                    primaryImageMediaId = product.imageMediaId,
                    variants = listOf(
                        CreateVariantRequest(
                            sku = product.sku,
                            mrpMinor = product.mrp,
                            sellingPriceMinor = product.sellingPrice,
                            stockQty = product.openingStock,
                        ),
                    ),
                ),
            )
        }.map { dto ->
            SellerProduct(
                id = dto.id,
                title = dto.title,
                status = dto.status,
                approvalStatus = dto.approvalStatus,
                rejectionReason = dto.rejectionReason,
                imageUrl = dto.thumbnailUrl ?: dto.imageUrl,
            )
        }

    /** Submits a product for moderation review. */
    suspend fun submitProduct(productId: String): CommerceResult<Unit> =
        call { api.submitProduct(productId) }.map { }

    /**
     * Changes a variant's price and availability.
     *
     * Only what is non-null is sent, so a price edit does not silently
     * re-assert a status the seller did not touch. Money is paise: the rupee
     * form is never sent, and the server refuses a body whose two shapes of
     * the same price disagree rather than choosing what the buyer pays.
     */
    suspend fun updateVariant(
        variantId: String,
        sellingPrice: Paise? = null,
        mrp: Paise? = null,
        status: String? = null,
    ): CommerceResult<Unit> =
        call {
            api.updateVariant(
                variantId,
                UpdateVariantRequest(
                    sellingPriceMinor = sellingPrice,
                    mrpMinor = mrp,
                    status = status,
                ),
            )
        }.map { }

    /** One of the caller's own variants. */
    suspend fun variant(variantId: String): CommerceResult<SellerVariant> =
        call { api.sellerVariant(variantId) }.map { dto ->
            SellerVariant(
                variantId = dto.variantId,
                title = dto.title,
                sku = dto.sku,
                sellingPrice = dto.sellingPriceMinor,
                mrp = dto.mrpMinor,
                status = dto.status,
                stock = StockLevel(
                    variantId = dto.variantId,
                    total = dto.totalQty,
                    reserved = dto.reservedQty,
                    available = dto.available,
                ),
            )
        }

    /** What the shop still needs before it can be reviewed. */
    suspend fun sellerReadiness(): CommerceResult<SellerReadiness> =
        call { api.sellerReadiness() }.map { dto ->
            SellerReadiness(
                ready = dto.ready,
                missing = dto.missing.map(SellerRequirement::from),
            )
        }

    /**
     * Saves where the seller is paid.
     *
     * Either a bank account (number + IFSC) or a UPI id satisfies the server's
     * readiness check. Both are accepted; neither is.
     */
    suspend fun savePayout(account: PayoutAccount): CommerceResult<Unit> =
        call {
            api.savePayout(
                PayoutRequest(
                    accountHolderName = account.accountHolderName.trim(),
                    accountNumber = account.accountNumber.trim(),
                    bankName = account.bankName?.trim()?.takeIf { it.isNotBlank() },
                    ifscCode = account.ifscCode?.trim()?.uppercase()?.takeIf { it.isNotBlank() },
                    upiId = account.upiId?.trim()?.takeIf { it.isNotBlank() },
                ),
            )
        }.map { }

    /**
     * Attaches KYC documents.
     *
     * Each media id is verified server-side against media-service — it must
     * belong to the caller, be ready and have passed moderation. That check is
     * the reason a seller cannot point their KYC at somebody else's uploaded
     * identity document, so a refusal here is a real answer and not a
     * formality.
     */
    suspend fun saveDocuments(documents: List<SellerDocument>): CommerceResult<Unit> =
        call {
            api.saveDocuments(
                SaveDocumentsRequest(
                    documents = documents.map { d ->
                        DocumentInput(
                            documentType = d.type.wire,
                            mediaId = d.mediaId,
                            documentNumber = d.documentNumber
                                ?.trim()
                                ?.uppercase()
                                ?.takeIf { it.isNotBlank() },
                        )
                    },
                ),
            )
        }.map { }
    private fun toStock(dto: StockDto) = StockLevel(
        variantId = dto.variantId,
        total = dto.totalQty,
        reserved = dto.reservedQty,
        available = dto.available,
    )
    // ─── Plumbing ────────────────────────────────────────────────────

    @Suppress("TooGenericExceptionCaught")
    private suspend fun <T> call(block: suspend () -> Response<ApiEnvelope<T>>): CommerceResult<T> =
        try {
            val response = block()
            val body = response.body()
            when {
                response.isSuccessful && body?.data != null ->
                    CommerceResult.Success(body.data!!)

                response.isSuccessful ->
                    // A 204 or an empty-bodied 200. Unit-returning calls land
                    // here legitimately.
                    @Suppress("UNCHECKED_CAST")
                    CommerceResult.Success(Unit as T)

                else -> {
                    // C3-LB-2. The error code is read from the ERROR body.
                    //
                    // This used to read `body?.error?.code` — but Retrofit
                    // gives `body()` only for a 2xx; on any 4xx/5xx it is null
                    // and the payload is in `errorBody()`. So the code was
                    // ALWAYS null on exactly the responses that carry one, and
                    // every typed server error — PRICE_CHANGED, OUT_OF_STOCK,
                    // IDEMPOTENCY_CONFLICT, COD_NOT_SUPPORTED — fell through to
                    // `Unexpected` and rendered as "Something went wrong."
                    //
                    // The consequence for this pass: PRICE_CHANGED could not
                    // block the way C3-LB-2 requires, because the client never
                    // saw it. The whole typed-error contract, and the checkout
                    // states built on it, were unreachable.
                    val raw = response.errorBody()?.string()
                    val err = parseError(raw)
                    CommerceResult.Failure(mapError(err?.first, err?.second, raw))
                }
            }
        } catch (t: Throwable) {
            CommerceResult.Failure(CommerceError.Network(t))
        }

    /**
     * Maps the server's stable error code onto a renderable failure.
     *
     * The `details` payload is parsed for the two codes that carry per-line
     * information, because a cart that says "something is out of stock"
     * without saying WHICH is not actionable.
     */
    @Suppress("CyclomaticComplexMethod")
    private fun mapError(code: String?, message: String?, rawBody: String?): CommerceError {
        val details = parseDetails(rawBody)
        return when (code) {
            "OUT_OF_STOCK" -> CommerceError.OutOfStock(
                details?.get("lines")?.jsonArray?.map { el ->
                    val o = el.jsonObject
                    UnavailableLine(
                        variantId = o["variant_id"]?.jsonPrimitive?.content.orEmpty(),
                        productId = o["product_id"]?.jsonPrimitive?.content.orEmpty(),
                        title = o["product_title"]?.jsonPrimitive?.content.orEmpty(),
                        requested = o["requested"]?.jsonPrimitive?.content?.toIntOrNull() ?: 0,
                        available = o["available"]?.jsonPrimitive?.content?.toIntOrNull() ?: 0,
                    )
                }.orEmpty()
            )

            "PRICE_CHANGED" -> CommerceError.PriceChanged(
                lines = details?.get("lines")?.jsonArray?.map { el ->
                    val o = el.jsonObject
                    ChangedLine(
                        variantId = o["variant_id"]?.jsonPrimitive?.content.orEmpty(),
                        was = Paise(o["was_minor"]?.jsonPrimitive?.content?.toLongOrNull() ?: 0L),
                        now = Paise(o["now_minor"]?.jsonPrimitive?.content?.toLongOrNull() ?: 0L),
                    )
                }.orEmpty(),
                newTotal = details?.get("new_total_minor")?.jsonPrimitive?.content
                    ?.toLongOrNull()?.let(::Paise),
            )

            "QUOTE_STALE" -> CommerceError.QuoteStale
            "QUOTE_EXPIRED", "QUOTE_CONSUMED" -> CommerceError.QuoteExpired
            "NOT_SERVICEABLE" -> CommerceError.NotServiceable(message)
            "IDEMPOTENCY_CONFLICT" -> CommerceError.IdempotencyConflict
            "PRODUCT_UNAVAILABLE" -> CommerceError.ProductUnavailable
            "MULTIPLE_SELLERS" -> CommerceError.MultipleSellers
            "COUPON_UNAVAILABLE" -> CommerceError.CouponUnavailable
            "CART_EMPTY" -> CommerceError.CartEmpty
            "COD_NOT_SUPPORTED" -> CommerceError.CodNotSupported
            "ORDER_NOT_FOUND", "ADDRESS_NOT_FOUND" -> CommerceError.OrderNotFound
            "CANCEL_NOT_PERMITTED" -> CommerceError.CancelNotPermitted
            "TRY_AGAIN" -> CommerceError.TryAgain
            "IDEMPOTENCY_KEY_REQUIRED" ->
                // A client bug, not a user-facing condition: the app must
                // always send the header.
                CommerceError.Unexpected(code, "checkout was sent without an Idempotency-Key")

            else -> CommerceError.Unexpected(code.orEmpty(), message.orEmpty())
        }
    }

    /**
     * Pulls `error.code` and `error.message` out of an error body.
     *
     * Returns null when the body is absent or not the expected envelope, in
     * which case mapError falls through to a generic failure — which is the
     * right answer for a gateway timeout or an HTML error page, and the wrong
     * one only when the server DID send a code, which is what this exists to
     * stop happening.
     */
    private fun parseError(rawBody: String?): Pair<String?, String?>? = runCatching {
        val error = rawBody?.let { Json.parseToJsonElement(it) }
            ?.jsonObject?.get("error")?.jsonObject ?: return@runCatching null
        error["code"]?.jsonPrimitive?.content to error["message"]?.jsonPrimitive?.content
    }.getOrNull()

    private fun parseDetails(rawBody: String?) = runCatching {
        rawBody?.let { Json.parseToJsonElement(it) }
            ?.jsonObject?.get("error")?.jsonObject?.get("details")?.jsonObject
    }.getOrNull()

    private fun toCart(dto: CartDto) = Cart(
        items = dto.items.map { l ->
            CartLine(
                variantId = l.variantId,
                productId = l.productId,
                title = l.title,
                imageMediaId = l.imageMediaId,
                imageUrl = l.imageUrl,
                options = emptyList(),
                quantity = l.quantity,
                unitPrice = l.unitPriceMinor,
                lineTotal = l.lineTotalMinor,
                priceChangedFrom = l.priceWasMinor,
                availableQty = l.availableQty,
            )
        },
        subtotal = dto.subtotalMinor,
        sellerId = dto.sellerId,
        sellerName = dto.sellerName,
    )

    private fun toAddress(dto: AddressDto) = Address(
        id = dto.id,
        label = dto.label,
        contactName = dto.contactName,
        phone = dto.phone,
        line1 = dto.line1,
        line2 = dto.line2,
        landmark = dto.landmark,
        city = dto.city,
        state = dto.state,
        postalCode = dto.postalCode,
        isDefault = dto.isDefault,
    )

    private fun toOrder(dto: OrderDto) = Order(
        id = dto.id,
        orderNumber = dto.orderNumber,
        status = OrderStatus.from(dto.status),
        paymentStatus = PaymentStatus.from(dto.paymentStatus),
        placedAtEpochSeconds = dto.createdAtEpoch,
        breakdown = PriceBreakdown(
            subtotal = dto.subtotalMinor,
            discount = dto.discountMinor,
            shipping = dto.shippingMinor,
            tax = dto.taxMinor,
            total = dto.totalMinor,
            currency = dto.currency,
        ),
        lines = dto.items.map { l ->
            OrderLine(
                productId = l.productId,
                variantId = l.variantId,
                title = l.title,
                imageMediaId = l.imageMediaId,
                imageUrl = l.imageUrl,
                options = emptyList(),
                quantity = l.quantity,
                unitPrice = l.unitPriceMinor,
                lineTotal = l.finalPriceMinor,
            )
        },
        deliveryAddress = dto.deliveryAddress?.let(::toAddress)
            ?: Address("", "", "", "", "", null, null, "", "", "", false),
        canCancel = dto.canCancel,
        trackingUrl = dto.trackingUrl,
    )
}

/** Default catalogue page size. Matches the server's own default. */
const val PAGE_SIZE = 20

/** The result of a successful checkout. */
data class CheckoutOutcome(
    val orderId: String,
    val orderNumber: String,
    val breakdown: PriceBreakdown,
    val paymentIntentId: String?,
    val clientSession: Map<String, String>,
)
