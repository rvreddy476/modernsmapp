package com.us.android.core.commerce.model

import com.us.android.core.facear.TryOnDescriptor

/**
 * Domain models for the Commerce P0 loop.
 *
 * Every money field is [Paise]. There is no `Double` in this file and there
 * must never be one: the CI money gate rejects it, and the type system makes
 * it a compile error before CI ever runs.
 *
 * These are DOMAIN types, deliberately separate from the wire DTOs in
 * `network/`. The separation earns its keep here because the API is
 * mid-migration — money fields exist in both rupee and minor forms during
 * the dual-write window — and the domain must only ever see the minor one.
 */

// ─── Catalog ─────────────────────────────────────────────────────────

data class Product(
    val id: String,
    val title: String,
    val description: String?,
    val brandName: String?,
    val primaryImageMediaId: String?,
    /** Resolved by the server; null when media-service could not answer. */
    val imageUrl: String? = null,
    val thumbnailUrl: String? = null,
    val sellerId: String,
    val sellerName: String?,
    val avgRating: Float,
    val reviewCount: Int,
    val variants: List<Variant>,
    /**
     * Virtual try-on, or null when the product does not offer one — which is
     * also what an older server sending no `try_on` at all means.
     *
     * The TYPE comes from `:core:facear`, not from this file, because the
     * module that parses the wire and the module that puts the effect on a
     * face have to agree on what a try-on kind and a shade are, and a second
     * copy here is how they stop agreeing. That edge costs nothing at compile
     * time: the Banuba SDK is `implementation` inside `:core:facear`, so this
     * module gains the vocabulary and not the ability to call a camera.
     */
    val tryOn: TryOnDescriptor? = null,
)

data class Variant(
    val id: String,
    val sku: String,
    /** e.g. "Size" / "M", "Colour" / "Blue". */
    val options: List<VariantOption>,
    /** Struck-through price. */
    val mrp: Paise,
    /** What the buyer pays. GST-INCLUSIVE — see [PriceBreakdown]. */
    val sellingPrice: Paise,
    val inStock: Boolean,
    val availableQty: Int,
)

data class VariantOption(val name: String, val value: String)

/**
 * A catalogue row.
 *
 * Deliberately NOT [Product]: the list endpoint returns a price RANGE and no
 * variants, and modelling that as a Product with an empty variant list would
 * let a screen render an "Add to cart" button for something it cannot add —
 * the buyer must choose a variant on the detail screen first.
 */
data class ProductSummary(
    val id: String,
    val title: String,
    val brandName: String?,
    val primaryImageMediaId: String?,
    /** Resolved by the server; null when media-service could not answer. */
    val imageUrl: String? = null,
    val thumbnailUrl: String? = null,
    /** The cheapest variant's price. GST-inclusive, like every price here. */
    val fromPrice: Paise,
    val mrp: Paise,
    val avgRating: Float,
    val reviewCount: Int,
    val inStock: Boolean,
    /**
     * The saving as whole percent, or null when there is none to show.
     *
     * Server-first: the repository takes `discount_pct` when the server sends
     * one and falls back to [discountPercent] only when it does not, so the
     * client and the server can never print two different numbers for a
     * server that has an opinion.
     */
    val discountPct: Int? = null,
    /** Whether the caller has saved this product. Server-owned. */
    val favourite: Boolean = false,
)

/**
 * The saving between a struck-through price and what is charged, as whole
 * percent — floored, so a 24.9% saving is never advertised as 25.
 *
 * Null when there is nothing honest to show: an MRP at or below the price is
 * not a discount, and a zero MRP is a missing value rather than a 100% one.
 *
 * This is the ONE piece of price arithmetic the client performs, and it
 * exists only as a fallback for a server that does not publish `discount_pct`
 * yet. Everything else about money is read, never derived.
 */
fun discountPercent(price: Paise, mrp: Paise): Int? {
    if (mrp <= price || !mrp.isPositive) return null
    val saved = mrp.value - price.value
    val percent = (saved * PERCENT) / mrp.value
    return percent.toInt().takeIf { it > 0 }
}

private const val PERCENT = 100L

/** One node of the shop's taxonomy. */
data class Category(
    val id: String,
    val name: String,
    val slug: String,
    val parentId: String?,
    val imageUrl: String?,
    val featured: Boolean,
    /**
     * Live published products in this category's subtree. Zero means the
     * tile opens onto nothing, so the strip dims it rather than inviting a
     * tap into an empty list. Defaulted because the field is additive and
     * the fakes and fixtures that build categories predate it.
     */
    val productCount: Int = 0,
) {
    /** A top-level category — the strip and the "Shop by category" grid show these. */
    val isTopLevel: Boolean get() = parentId == null

    /** Nothing to browse behind this tile. Still tappable: the browse page's own empty state explains. */
    val isEmpty: Boolean get() = productCount <= 0
}

/**
 * Where a landing-page banner goes.
 *
 * A closed type, because a banner whose target this build cannot open must
 * render as a picture rather than as a control that does nothing — see
 * [HomeBanner.tappable].
 */
sealed interface BannerTarget {
    data class OfCategory(val categoryId: String) : BannerTarget
    data class OfProduct(val productId: String) : BannerTarget
    data class OfSearch(val query: String) : BannerTarget
    data object None : BannerTarget

    companion object {
        fun from(type: String?, id: String?): BannerTarget {
            val target = id?.takeIf { it.isNotBlank() } ?: return None
            return when (type) {
                "category" -> OfCategory(target)
                "product" -> OfProduct(target)
                "search" -> OfSearch(target)
                else -> None
            }
        }
    }
}

data class HomeBanner(
    val id: String,
    val title: String,
    val subtitle: String?,
    val imageUrl: String?,
    val target: BannerTarget,
) {
    val tappable: Boolean get() = target != BannerTarget.None
}

/** One named shelf on the landing page: "Deals of the day" and its products. */
data class HomeSection(
    val key: String,
    val title: String,
    val products: List<ProductSummary>,
)

/**
 * The landing page as the server describes it.
 *
 * [EMPTY] is what a 404 becomes. Every section on the page hides itself when
 * it has nothing, so an absent `/home` degrades to the category strip plus
 * the paged grid rather than to a shop full of empty shelves.
 */
data class StoreHome(
    val banners: List<HomeBanner>,
    val sections: List<HomeSection>,
) {
    companion object {
        val EMPTY = StoreHome(emptyList(), emptyList())
    }
}

/** One image on a product, in the seller's chosen order. */
data class ProductImage(
    val mediaId: String,
    val url: String?,
    val sortOrder: Int,
)

/**
 * One node of `GET /categories?tree=true`, nested.
 *
 * [listable] is whether a product may sit directly on this node. A heading
 * that only groups others ("Books") is not, and a seller choosing where a
 * listing goes must be offered the leaves ("Textbooks"), never the heading.
 */
data class CategoryNode(
    val id: String,
    val name: String,
    val listable: Boolean,
    val active: Boolean,
    val children: List<CategoryNode>,
)

/**
 * A category a seller can list a product under, with the path a person reads
 * ("Books › Textbooks"), so two leaves with the same name under different
 * headings stay distinguishable in a picker.
 */
data class CategoryChoice(
    val id: String,
    val label: String,
)

/**
 * The categories a product may be listed under, from the tree: every ACTIVE,
 * LISTABLE node with no listable children, labelled with its path. Pure, so
 * the rule is a table test. Order is the server's (display order, depth
 * first), which is the order a picker shows.
 *
 * 2026-09-30: a node that is listable but has listable children is still
 * offered — "Phones" can hold a listing that fits no sub-category — but a
 * heading that is not listable never is, whatever its children say.
 */
fun listableCategories(roots: List<CategoryNode>): List<CategoryChoice> {
    val out = mutableListOf<CategoryChoice>()
    fun walk(node: CategoryNode, path: List<String>) {
        if (!node.active) return
        val here = path + node.name
        if (node.listable && node.id.isNotBlank()) out += CategoryChoice(node.id, here.joinToString(PATH_SEPARATOR))
        node.children.forEach { walk(it, here) }
    }
    roots.forEach { walk(it, emptyList()) }
    return out
}

/**
 * The same choice list from the FLAT taxonomy, for a server that predates
 * `?tree=true`. Without `is_listable` the only honest rule is "a node nobody
 * hangs a child under": a category with children is treated as a heading.
 */
fun listableCategoriesFromFlat(flat: List<Category>): List<CategoryChoice> {
    val byId = flat.associateBy { it.id }
    val parents = flat.mapNotNull { it.parentId }.toSet()
    fun pathOf(c: Category): String {
        val names = ArrayDeque<String>()
        var cur: Category? = c
        var guard = 0
        while (cur != null && guard++ < MAX_DEPTH) {
            names.addFirst(cur.name)
            cur = cur.parentId?.let(byId::get)
        }
        return names.joinToString(PATH_SEPARATOR)
    }
    return flat.filter { it.id.isNotBlank() && it.id !in parents }.map { CategoryChoice(it.id, pathOf(it)) }
}

private const val PATH_SEPARATOR = " › "
private const val MAX_DEPTH = 16

/** One page of catalogue results. */
data class ProductPage(
    val items: List<ProductSummary>,
    /**
     * Opaque server cursor. Null means this is the last page — the list must
     * stop asking, or it paginates forever against an empty tail.
     */
    val nextCursor: String?,
)

// ─── Cart ────────────────────────────────────────────────────────────

data class Cart(
    val items: List<CartLine>,
    val subtotal: Paise,
    /** The single seller this cart belongs to. D2: P0 carts are single-seller. */
    val sellerId: String?,
    val sellerName: String?,
)

data class CartLine(
    val variantId: String,
    val productId: String,
    val title: String,
    val imageMediaId: String?,
    /** Resolved by the server; null when media-service could not answer. */
    val imageUrl: String? = null,
    val options: List<VariantOption>,
    val quantity: Int,
    val unitPrice: Paise,
    val lineTotal: Paise,
    /** Set when the catalogue price moved since this line was added. */
    val priceChangedFrom: Paise? = null,
    val availableQty: Int? = null,
)

// ─── Address ─────────────────────────────────────────────────────────

data class Address(
    val id: String,
    val label: String,
    val contactName: String,
    val phone: String,
    val line1: String,
    val line2: String?,
    val landmark: String?,
    val city: String,
    val state: String,
    val postalCode: String,
    val isDefault: Boolean,
)

// ─── Pricing ─────────────────────────────────────────────────────────

/**
 * The price breakdown, exactly as the server computed it.
 *
 * The app NEVER computes a total. Every field arrives from the server, which
 * derives them inside the checkout transaction; the client's job is to render
 * them. This is why there is no `fun total() = subtotal - discount + ...`
 * here, tempting as it looks — a client-side total that disagrees with the
 * server is a support ticket at best.
 *
 * D1: catalogue prices are GST-INCLUSIVE, so [tax] is the component already
 * contained in [subtotal], not an addition to it. Rendering it as "+ GST"
 * would overstate the total by the tax amount.
 */
data class PriceBreakdown(
    val subtotal: Paise,
    val discount: Paise,
    val shipping: Paise,
    /** GST already included in the amounts above. Shown as "incl. GST". */
    val tax: Paise,
    val total: Paise,
    val currency: String = "INR",
)

// ─── Delivery quote ──────────────────────────────────────────────────

/**
 * A server-issued delivery quote.
 *
 * A4: obtained before checkout and consumed inside the checkout transaction.
 * It is bound server-side to the cart version, the address content, the
 * seller and the item set, so anything the customer changes afterwards
 * invalidates it — the app must re-quote rather than reuse.
 */
data class DeliveryQuote(
    val quoteId: String,
    /**
     * C3-LB-2: the COMPLETE server-computed price this quote represents.
     *
     * The client renders these figures and sends [PriceBreakdown.total] back
     * as `expected_total_minor`. It performs no arithmetic on them beyond
     * formatting — the previous version had no total to show, computed
     * `subtotal 0 + shipping`, and submitted that, which made every non-empty
     * cart fail with PRICE_CHANGED.
     */
    val breakdown: PriceBreakdown,
    val courierCode: String?,
    val expiresAtEpochSeconds: Long,
) {
    /** Convenience for screens that only need the delivery line. */
    val shipping: Paise get() = breakdown.shipping
}

// ─── Orders ──────────────────────────────────────────────────────────

data class Order(
    val id: String,
    val orderNumber: String,
    val status: OrderStatus,
    val paymentStatus: PaymentStatus,
    val placedAtEpochSeconds: Long,
    val breakdown: PriceBreakdown,
    val lines: List<OrderLine>,
    /**
     * The address as it was at purchase.
     *
     * LB-18: this is an immutable snapshot stored on the order, not a
     * pointer to the customer's current address. Editing a saved address
     * used to silently rewrite the delivery record of every past order,
     * including delivered ones and their GST invoices.
     */
    val deliveryAddress: Address,
    val canCancel: Boolean,
    val trackingUrl: String?,
    /**
     * Whether the server says a fresh payment intent would re-open this order
     * (contract §4.7). Null when the server did not say — a build older than
     * the retry — and the screen then offers "Pay now" on [OrderStatus.PAYMENT_FAILED]
     * and lets the intent route answer, including with OUT_OF_STOCK.
     */
    val canRetryPayment: Boolean? = null,
    /**
     * How many lines the order has and the first one's title, from the LIST
     * read, which sends no [lines] (2026-09-30). Zero and null on the detail
     * read; use [lineCount] and [firstLineTitle], which prefer the lines.
     */
    val itemCount: Int = 0,
    val firstItemTitle: String? = null,
) {
    /** The number of lines, from the lines when the read carried them, else the list's count. */
    val lineCount: Int get() = maxOf(lines.size, itemCount)

    /** The first line's title, from the lines when the read carried them, else the list's. */
    val firstLineTitle: String?
        get() = lines.firstOrNull()?.title?.takeIf { it.isNotBlank() }
            ?: firstItemTitle?.takeIf { it.isNotBlank() }

    /**
     * Whether "Pay now" is offered (2026-09-30). Awaiting payment always;
     * failed payment unless the server said it cannot be retried.
     */
    val canPayNow: Boolean
        get() = status == OrderStatus.PAYMENT_PENDING ||
            (status == OrderStatus.PAYMENT_FAILED && canRetryPayment != false)
}

/**
 * One page of the buyer's orders.
 *
 * [nextCursor] is the server's opaque continuation; null is the last page.
 * The list keeps it and asks for more at the end, rather than showing the
 * first twenty orders as though they were all of them.
 */
data class OrderPage(
    val items: List<Order>,
    val nextCursor: String?,
)

/**
 * The three-state payment read, `GET /orders/:id/payment` (contract §4.5).
 *
 * [refundStatus] outranks [state]: a capture that landed after the order
 * lapsed is being returned and must never render as a paid order.
 */
data class OrderPayment(
    val orderId: String,
    val state: OrderPaymentState,
    /** Null, or `pending`, `partially_refunded`, `refunded`. */
    val refundStatus: String?,
    val amount: Paise,
    val currency: String,
)

enum class OrderPaymentState {
    CONFIRMING,
    PAID,
    FAILED,
    UNKNOWN;

    companion object {
        fun from(raw: String): OrderPaymentState = when (raw.trim().lowercase()) {
            "confirming" -> CONFIRMING
            "paid" -> PAID
            "failed" -> FAILED
            // The server's vocabulary can grow ahead of a released app; an
            // unrecognised state keeps confirming rather than claiming one.
            else -> UNKNOWN
        }
    }
}

data class OrderLine(
    val productId: String,
    val variantId: String,
    val title: String,
    val imageMediaId: String?,
    /** Resolved by the server; null when media-service could not answer. */
    val imageUrl: String? = null,
    val options: List<VariantOption>,
    val quantity: Int,
    val unitPrice: Paise,
    val lineTotal: Paise,
)

enum class OrderStatus {
    PAYMENT_PENDING,
    PAYMENT_FAILED,

    /** LB-22: the reservation lapsed before payment; the hold is gone. */
    EXPIRED,
    CONFIRMED,
    PACKED,
    SHIPPED,
    OUT_FOR_DELIVERY,
    DELIVERED,
    CANCELLED,
    REFUND_PENDING,
    REFUNDED,
    UNKNOWN;

    companion object {
        fun from(raw: String): OrderStatus = when (raw) {
            "payment_pending" -> PAYMENT_PENDING
            "payment_failed" -> PAYMENT_FAILED
            "expired" -> EXPIRED
            "confirmed", "paid" -> CONFIRMED
            "packed" -> PACKED
            "shipped" -> SHIPPED
            "out_for_delivery" -> OUT_FOR_DELIVERY
            "delivered" -> DELIVERED
            "cancelled" -> CANCELLED
            "refund_pending" -> REFUND_PENDING
            "refunded" -> REFUNDED
            // An unrecognised status must not crash the order list. The
            // server's vocabulary can grow ahead of a released app.
            else -> UNKNOWN
        }
    }
}

enum class PaymentStatus {
    PENDING,

    /**
     * The PSP redirect came back and looked genuine, but nothing is
     * confirmed yet.
     *
     * A1: a client redirect is NEVER proof of payment. This state exists so
     * the UI can say "confirming your payment" and poll, rather than
     * claiming success from a callback the server has not verified.
     */
    AWAITING_CONFIRMATION,
    PAID,
    FAILED,
    REFUND_PENDING,
    REFUNDED,
    UNKNOWN;

    companion object {
        fun from(raw: String): PaymentStatus = when (raw) {
            "pending", "processing" -> PENDING
            "paid" -> PAID
            "failed" -> FAILED
            "refund_pending" -> REFUND_PENDING
            "refunded", "partially_refunded" -> REFUNDED
            else -> UNKNOWN
        }
    }
}

// ─── Payment handoff ─────────────────────────────────────────────────

/**
 * What the client needs to open the PSP sheet.
 *
 * LB-4: the app asks for a payment against an ORDER and receives a handle.
 * It never names an amount — that was the whole shape of the zero-rupee
 * exploit, where a client could open a 1-paise intent against a ₹10,000
 * order and then assert it had succeeded.
 */
data class PaymentHandle(
    val paymentIntentId: String,
    val amount: Paise,
    val currency: String,
    /** The PSP's own order handle, opaque to us. */
    val providerRef: String?,
    val status: PaymentStatus,
    /**
     * What the client SDK needs to open checkout. Server-sourced — see
     * PaymentHandleDto.clientSession.
     */
    val clientSession: Map<String, String> = emptyMap(),
)

// ─── Seller ──────────────────────────────────────────────────────────

/**
 * The onboarding/approval state machine, as a closed type.
 *
 * Modelled as an enum rather than the raw string because the seller UI has to
 * branch on it, and a typo in a `when (status) { "aproved" -> }` is a screen
 * that silently shows the wrong thing. [UNKNOWN] is the honest landing place
 * for a value this build has not seen — a server that adds a state must not
 * make the app crash or, worse, treat it as approved.
 */
enum class SellerStatus {
    DRAFT,
    SUBMITTED,
    UNDER_REVIEW,
    CHANGES_REQUIRED,
    APPROVED,
    REJECTED,
    SUSPENDED,
    DISABLED,
    UNKNOWN,
    ;

    /** Whether this seller may currently list and sell. */
    val canSell: Boolean get() = this == APPROVED

    /**
     * Whether the shop can be sent for review right now.
     *
     * Only from DRAFT and CHANGES_REQUIRED. A shop already submitted has
     * nothing to send, and offering the button again invites a seller to think
     * their first attempt did not land.
     */
    val canSubmit: Boolean get() = this == DRAFT || this == CHANGES_REQUIRED

    companion object {
        fun from(raw: String?): SellerStatus = when (raw?.lowercase()) {
            "draft" -> DRAFT
            "submitted" -> SUBMITTED
            "under_review" -> UNDER_REVIEW
            "changes_required" -> CHANGES_REQUIRED
            "approved" -> APPROVED
            "rejected" -> REJECTED
            "suspended" -> SUSPENDED
            "disabled" -> DISABLED
            else -> UNKNOWN
        }
    }
}

data class SellerProfile(
    val id: String,
    val storeName: String,
    val status: SellerStatus,
    val onboardingStep: Int,
    val state: String?,
    val city: String?,
    val postalCode: String?,
    val totalProducts: Int,
    val totalOrders: Int,
)

/**
 * A row of the seller's own catalogue, in whatever state it is in.
 *
 * [variants] carries the product's variants WITH THEIR OWN IDS when the
 * server sent them (contract §6). A stock or price edit addresses a variant,
 * never the product: the server looks up `product_variants.id`, and the
 * product id sent there is a variant that does not exist. Empty on a server
 * that predates the field; the repository then reads
 * `GET /products/:id/variants` before opening either screen.
 */
data class SellerProduct(
    val id: String,
    val title: String,
    val status: String,
    val approvalStatus: String,
    val rejectionReason: String?,
    val imageUrl: String?,
    val variants: List<Variant> = emptyList(),
    /** The list projection's shorthand for the single variant, when the server sends one. */
    val defaultVariantId: String? = null,
)

/**
 * A product's variants as known so far: the row's own, else what the server
 * sent back for the product. NEVER the product id — see [SellerProduct.variants].
 *
 * Pure, so the guard is a table test: a row carrying variants uses them; a
 * bare row uses the fetched list; a `default_variant_id` with no variant rows
 * is one variant with that id; nothing anywhere is an empty list, which the
 * screen renders as "this product has no variant to edit" rather than
 * guessing.
 */
fun variantsForEdit(product: SellerProduct, fetched: List<Variant>): List<Variant> = when {
    product.variants.isNotEmpty() -> product.variants
    fetched.isNotEmpty() -> fetched
    !product.defaultVariantId.isNullOrBlank() -> listOf(
        Variant(
            id = product.defaultVariantId,
            sku = "",
            options = emptyList(),
            mrp = Paise.ZERO,
            sellingPrice = Paise.ZERO,
            inStock = false,
            availableQty = 0,
        ),
    )
    else -> emptyList()
}

/**
 * What a variant chooser calls a variant: its options ("Size M · Blue"), or
 * its SKU when it has none, or a numbered fallback.
 */
fun Variant.chooserLabel(index: Int): String = when {
    options.isNotEmpty() -> options.joinToString(" · ") { it.value }
    sku.isNotBlank() -> sku
    else -> "Variant ${index + 1}"
}

/**
 * A variant's stock.
 *
 * [reserved] is a first-class field, not an implementation detail: those units
 * are promised to orders mid-checkout, and a seller writing down damaged stock
 * needs to know they cannot go below it.
 */
data class StockLevel(
    val variantId: String,
    val total: Int,
    val reserved: Int,
    val available: Int,
)

/**
 * The reasons a seller may give for a stock movement.
 *
 * Mirrors the server's allow-list, minus the return-QC codes, which belong to
 * the returns flow and are written by it rather than typed by a seller. A
 * movement with no stated cause is unauditable, so there is no "other".
 */
enum class StockReason(val wire: String, val label: String) {
    PURCHASE("purchase", "New stock arrived"),
    DAMAGE("damage", "Damaged"),
    THEFT("theft", "Lost or stolen"),
    RECOUNT("recount", "Recounted"),
    CORRECTION("correction", "Correcting a mistake"),
}

/** The seller's pickup point — the origin of every shipment they send. */
data class SellerAddress(
    val contactName: String,
    val phone: String,
    val line1: String,
    val line2: String?,
    val city: String,
    val state: String,
    val postalCode: String,
)

/**
 * One GST rate a seller can choose.
 *
 * [ratePercent] is the single number a seller recognises. CGST+SGST within a
 * state and IGST across one come to the same total, and showing three columns
 * invites a seller to think they are choosing between them when the place of
 * supply decides that.
 */
data class TaxClass(
    val id: String,
    val name: String,
    val ratePercent: Double,
)

/**
 * A new listing.
 *
 * Money is [Paise] throughout. This is the one place a human types the price
 * every subsequent sale is charged at, so there is no float anywhere on the
 * path from the keyboard to the database.
 */
data class NewProduct(
    val title: String,
    val description: String?,
    val taxClassId: String,
    val sku: String,
    val mrp: Paise,
    val sellingPrice: Paise,
    val openingStock: Int,
    val imageMediaId: String? = null,
    /**
     * Where the listing sits in browse (2026-09-30). Nullable so an older
     * caller's create is unchanged; the create screen refuses without one.
     */
    val categoryId: String? = null,
)

/** A variant as its seller sees it: what it costs and whether it is on sale. */
data class SellerVariant(
    val variantId: String,
    val title: String,
    val sku: String,
    val sellingPrice: Paise,
    val mrp: Paise,
    /** The variant's own switch — distinct from the product's approval state. */
    val status: String,
    val stock: StockLevel,
)

/**
 * What a shop still needs before it can be reviewed.
 *
 * The wire sends stable snake_case keys; they are turned into an enum here so
 * a screen branches on a closed set rather than string-matching. [UNKNOWN]
 * carries the raw key so a requirement this build has not seen is still shown
 * to the seller rather than silently dropped from the checklist.
 */
sealed interface SellerRequirement {
    data object StoreName : SellerRequirement
    data object Email : SellerRequirement
    data object PickupAddress : SellerRequirement
    data object PayoutAccount : SellerRequirement
    data object KycDocument : SellerRequirement
    data class Unknown(val key: String) : SellerRequirement

    companion object {
        fun from(key: String): SellerRequirement = when (key) {
            "store_name" -> StoreName
            "email" -> Email
            "pickup_address" -> PickupAddress
            "payout_account" -> PayoutAccount
            "kyc_document" -> KycDocument
            else -> Unknown(key)
        }
    }
}

data class SellerReadiness(
    val ready: Boolean,
    val missing: List<SellerRequirement>,
)

/** Where a seller is paid. */
data class PayoutAccount(
    val accountHolderName: String,
    val accountNumber: String,
    val bankName: String?,
    val ifscCode: String?,
    val upiId: String?,
)

/**
 * The kinds of identity document a reviewer accepts.
 *
 * Mirrors the server's `seller_documents_document_type_check`. A value outside
 * it is a 500 from a CHECK constraint, so the vocabulary is stated here rather
 * than typed free-form.
 */
enum class SellerDocumentType(val wire: String, val label: String) {
    PAN_CARD("pan_card", "PAN card"),
    AADHAAR("aadhaar", "Aadhaar"),
    PASSPORT("passport", "Passport"),
    GST_CERTIFICATE("gst_certificate", "GST certificate"),
    BUSINESS_REGISTRATION("business_registration", "Business registration"),
    ADDRESS_PROOF("address_proof", "Address proof"),
    CANCELLED_CHEQUE("cancelled_cheque", "Cancelled cheque"),
}

/** One uploaded identity document, ready to attach. */
data class SellerDocument(
    val type: SellerDocumentType,
    val mediaId: String,
    val documentNumber: String?,
)
