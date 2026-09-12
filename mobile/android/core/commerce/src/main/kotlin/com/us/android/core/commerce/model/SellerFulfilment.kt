package com.us.android.core.commerce.model

/**
 * The seller's side of an order: what they have to ship, what they may do
 * about it, what came back, and what they earned.
 *
 * Domain types, separate from the wire DTOs for the same reason as the rest
 * of this package: the server's seller structs were written for its web
 * dashboard and carry rupee mirrors, a flattened list row and a base64
 * address, none of which a screen should have to know about.
 */

// Orders

/**
 * One row of the seller's order list. The header plus the two numbers a
 * list needs; the lines themselves come with the detail.
 */
data class SellerOrderSummary(
    val id: String,
    val orderNumber: String,
    val status: OrderStatus,
    val paymentStatus: PaymentStatus,
    val paymentMethod: String?,
    /** What the buyer paid for the whole order. */
    val total: Paise,
    /** How many of THIS seller's lines are on it. Zero from a server that does not say. */
    val itemCount: Int,
    /** What this seller is owed for it, over their own lines. Zero from a server that does not say. */
    val sellerSubtotal: Paise,
    /** RFC 3339, as received. Formatted by the screen; null when the server sent none. */
    val placedAt: String?,
)

/**
 * One recorded move of an order's status, from `order_status_history`.
 *
 * [from] is null on the row that created the order. [actorType] is the
 * server's word for who moved it (`customer`, `seller`, `admin`, `system`),
 * kept raw because the vocabulary is the server's to grow. [rawTo] is the
 * status word as sent, for a status this build does not know yet.
 */
data class SellerOrderTransition(
    val from: OrderStatus?,
    val to: OrderStatus,
    val rawTo: String,
    val actorType: String,
    val notes: String?,
    /** RFC 3339, as received. */
    val at: String?,
)

/** One of the seller's lines on an order. */
data class SellerOrderLine(
    val itemId: String,
    val productId: String,
    val variantId: String,
    val title: String,
    val sku: String,
    val quantity: Int,
    val unitPrice: Paise,
    val lineTotal: Paise,
    val status: String,
    val imageUrl: String?,
)

/** The seller's shipment on an order, if one has been booked. */
data class SellerShipment(
    val id: String,
    val courier: String,
    val trackingNumber: String?,
    val trackingUrl: String?,
    val status: String,
    val shippedAt: String?,
    val deliveredAt: String?,
    val eta: String?,
)

/**
 * One order from the seller's side.
 *
 * [lines] and [sellerSubtotal] are THIS seller's share; [orderTotal] is what
 * the buyer paid for the whole order, which on a multi-seller order is more.
 * The two are both shown because a seller reconciling a payout needs the
 * first and a seller answering a buyer's question needs the second.
 */
data class SellerOrder(
    val id: String,
    val orderNumber: String,
    val status: OrderStatus,
    val paymentStatus: PaymentStatus,
    val paymentMethod: String?,
    val placedAt: String?,
    val updatedAt: String?,
    val cancellationReason: String?,
    val cancelledBy: String?,
    val lines: List<SellerOrderLine>,
    val shipment: SellerShipment?,
    val sellerSubtotal: Paise,
    val orderTotal: Paise,
    /**
     * The buyer's delivery address, as far as the server will say.
     *
     * Null when the snapshot was absent or unreadable. After the PII cutover
     * the seller card carries the ROUTING snapshot only, so a present
     * address can still lack a contact name and phone; the label is the
     * courier's, not the seller's, and the screen says so.
     */
    val deliveryAddress: Address?,
) {
    /** What this seller may do next. Mirrors the server; see [sellerActionsFor]. */
    val actions: List<SellerAction> get() = sellerActionsFor(status)
}

/**
 * The things a seller can do to an order.
 *
 * Three, not four. "Delivered" is deliberately absent: in the server's
 * transition matrix `shipped -> delivered` belongs to the SYSTEM actor (the
 * courier's webhook), never to the seller, and a button that lets a seller
 * declare their own parcel delivered would let them start the return clock
 * and the payout without the courier's say-so.
 */
enum class SellerAction {
    /** Mark the order packed and ready for pickup. */
    PACK,

    /** Hand it to a courier, with a tracking number the buyer can follow. */
    SHIP,

    /** Cancel with a reason the buyer will read. Refunds automatically if paid. */
    CANCEL,
}

/**
 * Which actions a seller may take from which order status.
 *
 * A MIRROR of the `actor_type = 'seller'` rows of `order_status_transitions`,
 * installed by commerce-service's
 * `database/migrations/010_gst_and_state_machine.sql` and enforced there by
 * the `enforce_order_transition` trigger. Kept as data next to a named
 * function, like the server's own `customerCancellableStatuses`, so the
 * buttons on the detail screen are the matrix's actual precondition rather
 * than a second, drifting opinion.
 *
 * The rows, verbatim:
 *
 *     ('confirmed','packed','seller')
 *     ('confirmed','cancelled','seller')
 *     ('packed','shipped','seller')
 *     ('packed','cancelled','seller')
 *
 * Note what is absent: nothing from `shipped` onwards. Once the parcel is
 * with the courier only an admin may intervene, and that path is an audited
 * exception rather than a self-service button. The server refuses anything
 * outside this table regardless; hiding the button is a courtesy, not the
 * control.
 */
fun sellerActionsFor(status: OrderStatus): List<SellerAction> = when (status) {
    OrderStatus.CONFIRMED -> listOf(SellerAction.PACK, SellerAction.CANCEL)
    OrderStatus.PACKED -> listOf(SellerAction.SHIP, SellerAction.CANCEL)
    else -> emptyList()
}

// Returns

/**
 * A return request's lifecycle, as a closed type.
 *
 * Only [REQUESTED] is actionable: the server refuses approve and reject from
 * any other state and treats a repeat of the same decision as idempotent.
 */
enum class ReturnStatus {
    REQUESTED,
    APPROVED,
    REJECTED,
    REFUNDED,
    CLOSED,
    UNKNOWN,
    ;

    /** Whether the seller still has a decision to make. */
    val awaitingDecision: Boolean get() = this == REQUESTED

    companion object {
        fun from(raw: String?): ReturnStatus = when (raw?.lowercase()) {
            "requested" -> REQUESTED
            "approved" -> APPROVED
            "rejected" -> REJECTED
            "refunded" -> REFUNDED
            "closed" -> CLOSED
            else -> UNKNOWN
        }
    }
}

/** One item in the returns inbox: the request plus what it is about. */
data class SellerReturn(
    val id: String,
    val orderId: String,
    val orderNumber: String?,
    val status: ReturnStatus,
    val reasonCode: String,
    val reasonDescription: String?,
    val requestedAt: String?,
    val rejectionReason: String?,
    /** The refund the seller is debited on approval. Null until the server has decided one. */
    val refundAmount: Paise?,
    /** The returned line, when the server could join it. */
    val itemTitle: String?,
    val itemSku: String?,
    val quantity: Int?,
    val lineTotal: Paise?,
    val imageUrl: String?,
)

// Earnings

/** One delivered prepaid line, with the server's payout arithmetic. */
data class SellerEarning(
    val orderItemId: String,
    val orderId: String,
    val orderNumber: String,
    val productTitle: String,
    val sku: String,
    val quantity: Int,
    val gross: Paise,
    val commission: Paise,
    val platformFee: Paise,
    val tds: Paise,
    val net: Paise,
    val paymentMethod: String?,
    val status: String,
    val deliveredAt: String?,
)
