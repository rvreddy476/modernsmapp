package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.ReturnStatus
import com.us.android.core.commerce.network.OrderStatusHistoryDto
import com.us.android.core.commerce.network.SellerEarningDto
import com.us.android.core.commerce.network.SellerFulfilmentResultDto
import com.us.android.core.commerce.network.SellerOrderCardDto
import com.us.android.core.commerce.network.SellerOrderDto
import com.us.android.core.commerce.network.SellerOrderHistoryDto
import com.us.android.core.commerce.network.SellerReturnCardDto
import com.us.android.core.commerce.network.ShipmentDto
import com.us.android.core.commerce.network.ShipmentEventDto
import com.us.android.core.commerce.network.ShipmentsDto
import com.us.android.core.commerce.network.rupeeTextToPaise
import com.us.android.core.commerce.repository.decodeAddressSnapshot
import com.us.android.core.commerce.repository.toSellerOrder
import com.us.android.core.commerce.repository.toSellerReturn
import com.us.android.core.commerce.repository.toSummary
import com.us.android.core.commerce.repository.toTransition
import com.us.android.core.network.di.NetworkModule
import org.junit.Test
import java.util.Base64

/**
 * The seller surfaces decode from what commerce-service actually writes.
 *
 * These payloads are transcribed from the server's structs rather than
 * invented, because the shapes are the unusual part: the list row lays the
 * seller's counters flat beside the order header, the address is a base64
 * JSON blob, and the earnings amounts are rupee floats. Each also decodes
 * WITHOUT its optional parts, since every one of them is `omitempty` on the
 * wire.
 */
class SellerFulfilmentDtoTest {

    private val json = NetworkModule.provideJson()

    // Orders

    @Test
    fun `an order row reads minor money and ignores the rupee mirrors`() {
        val dto = json.decodeFromString(
            SellerOrderDto.serializer(),
            """
            {"id":"o-1","order_number":"MS-1001","status":"confirmed","payment_status":"paid",
             "payment_method":"upi","subtotal":0,"final_amount":0,
             "subtotal_minor":92900,"shipping_minor":7000,"total_minor":99900,
             "currency_code":"INR","created_at":"2026-09-12T10:15:00Z",
             "item_count":2,"seller_subtotal_minor":92900}
            """.trimIndent(),
        )

        assertThat(dto.totalMinor).isEqualTo(Paise(99900))
        assertThat(dto.subtotalMinor).isEqualTo(Paise(92900))
        assertThat(dto.paymentMethod).isEqualTo("upi")
        assertThat(dto.createdAt).isEqualTo("2026-09-12T10:15:00Z")
        // The seller's counters sit flat beside the header: SellerOrderRow
        // embeds postgres.Order, so they are siblings, not a nested object.
        val row = dto.toSummary()
        assertThat(row.itemCount).isEqualTo(2)
        assertThat(row.sellerSubtotal).isEqualTo(Paise(92900))
        assertThat(row.total).isEqualTo(Paise(99900))
        assertThat(row.paymentMethod).isEqualTo("upi")
    }

    @Test
    fun `an order row from a server without the counters still decodes`() {
        // The same DTO is the `order` of the detail card and of a return
        // card, neither of which carries item_count; zero, never a failure.
        val row = json.decodeFromString(
            SellerOrderDto.serializer(),
            """{"id":"o-1","status":"confirmed","total_minor":99900}""",
        ).toSummary()

        assertThat(row.itemCount).isEqualTo(0)
        assertThat(row.sellerSubtotal).isEqualTo(Paise.ZERO)
        assertThat(row.paymentMethod).isNull()
    }

    @Test
    fun `a detail card with a shipment and an address decodes fully`() {
        val address = Base64.getEncoder().encodeToString(
            """{"contact_name":"Asha","phone":"9000000000","address_line_1":"12 MG Road",
               "city":"Bengaluru","state":"Karnataka","postal_code":"560001"}""".toByteArray(),
        )
        val dto = json.decodeFromString(
            SellerOrderCardDto.serializer(),
            """
            {"order":{"id":"o-1","order_number":"MS-1001","status":"packed","payment_status":"paid",
                      "total_minor":99900},
             "items":[{"id":"i-1","product_id":"p-1","variant_id":"v-1","product_title":"Kettle",
                       "sku":"KET-1","quantity":2,"unit_price_minor":46450,"final_price_minor":92900,
                       "status":"confirmed","image_url":"https://obj/k.jpg"}],
             "shipment":{"id":"s-1","order_id":"o-1","seller_id":"sel-1","courier":"delhivery",
                         "tracking_number":"DL123","courier_order_id":null,"label_url":null,
                         "tracking_url":"https://t/DL123","status":"in_transit","eta":null,
                         "shipped_at":"2026-09-12T12:00:00Z","delivered_at":null,
                         "last_event_at":"2026-09-12T12:00:00Z",
                         "created_at":"2026-09-12T11:59:00Z","updated_at":"2026-09-12T12:00:00Z"},
             "seller_subtotal_minor":92900,"seller_subtotal":929.0,
             "delivery_address":"$address"}
            """.trimIndent(),
        )

        val order = dto.toSellerOrder()
        assertThat(order.status).isEqualTo(OrderStatus.PACKED)
        assertThat(order.sellerSubtotal).isEqualTo(Paise(92900))
        assertThat(order.orderTotal).isEqualTo(Paise(99900))
        assertThat(order.lines.single().lineTotal).isEqualTo(Paise(92900))
        assertThat(order.lines.single().imageUrl).isEqualTo("https://obj/k.jpg")
        // snake_case keys: postgres.Shipment carries json tags now. Pinned,
        // because a slide back to the Go field names would silently empty
        // every one of these.
        assertThat(order.shipment!!.id).isEqualTo("s-1")
        assertThat(order.shipment!!.courier).isEqualTo("delhivery")
        assertThat(order.shipment!!.trackingNumber).isEqualTo("DL123")
        assertThat(order.shipment!!.trackingUrl).isEqualTo("https://t/DL123")
        assertThat(order.shipment!!.status).isEqualTo("in_transit")
        assertThat(order.shipment!!.shippedAt).isEqualTo("2026-09-12T12:00:00Z")
        assertThat(order.shipment!!.deliveredAt).isNull()
        assertThat(order.shipment!!.eta).isNull()
        assertThat(order.deliveryAddress!!.city).isEqualTo("Bengaluru")
        assertThat(order.deliveryAddress!!.postalCode).isEqualTo("560001")
        assertThat(order.deliveryAddress!!.contactName).isEqualTo("Asha")
    }

    @Test
    fun `a shipment in the old Go field casing decodes to nothing, not to a crash`() {
        // The shape the server USED to write. Every field must fall to its
        // default: this is the failure mode the snake_case pin above guards.
        val dto = json.decodeFromString(
            ShipmentDto.serializer(),
            """{"ID":"s-1","Courier":"delhivery","TrackingNumber":"DL123","Status":"in_transit"}""",
        )

        assertThat(dto.id).isEmpty()
        assertThat(dto.courier).isEmpty()
        assertThat(dto.trackingNumber).isNull()
    }

    @Test
    fun `the ship answer is a shipments list and each carries its events shape`() {
        val dto = json.decodeFromString(
            ShipmentsDto.serializer(),
            """
            {"shipments":[{"id":"s-1","order_id":"o-1","seller_id":"sel-1","courier":"delhivery",
                           "tracking_number":"DL123","courier_order_id":"C-9","label_url":"https://l/1",
                           "tracking_url":"https://t/DL123","status":"created","eta":null,
                           "shipped_at":null,"delivered_at":null,"last_event_at":null,
                           "created_at":"2026-09-12T11:59:00Z","updated_at":"2026-09-12T11:59:00Z"}]}
            """.trimIndent(),
        )

        val shipment = dto.shipments.single()
        assertThat(shipment.courierOrderId).isEqualTo("C-9")
        assertThat(shipment.labelUrl).isEqualTo("https://l/1")
        assertThat(shipment.status).isEqualTo("created")
        assertThat(shipment.shippedAt).isNull()

        val event = json.decodeFromString(
            ShipmentEventDto.serializer(),
            """
            {"id":"e-1","shipment_id":"s-1","status":"in_transit","location":"Bengaluru hub",
             "remark":null,"occurred_at":"2026-09-12T12:00:00Z","created_at":"2026-09-12T12:00:05Z"}
            """.trimIndent(),
        )
        assertThat(event.shipmentId).isEqualTo("s-1")
        assertThat(event.location).isEqualTo("Bengaluru hub")
        assertThat(event.remark).isNull()
        assertThat(event.occurredAt).isEqualTo("2026-09-12T12:00:00Z")
    }

    @Test
    fun `pack and cancel answer the order id, the landing status and whether it was applied`() {
        val first = json.decodeFromString(
            SellerFulfilmentResultDto.serializer(),
            """{"order_id":"o-1","status":"packed","applied":true}""",
        )
        assertThat(first.orderId).isEqualTo("o-1")
        assertThat(first.status).isEqualTo("packed")
        assertThat(first.applied).isTrue()

        // A repeat: still a 200, still decodes, just says nothing moved.
        val repeat = json.decodeFromString(
            SellerFulfilmentResultDto.serializer(),
            """{"order_id":"o-1","status":"packed","applied":false}""",
        )
        assertThat(repeat.applied).isFalse()

        // A cancel on a paid order lands in refund_pending, not cancelled.
        val cancel = json.decodeFromString(
            SellerFulfilmentResultDto.serializer(),
            """{"order_id":"o-1","status":"refund_pending","applied":true}""",
        )
        assertThat(cancel.status).isEqualTo("refund_pending")
    }

    @Test
    fun `the order history decodes oldest first with the first row's from status absent`() {
        val dto = json.decodeFromString(
            SellerOrderHistoryDto.serializer(),
            """
            {"order_id":"o-1","history":[
              {"id":"h-1","order_id":"o-1","to_status":"payment_pending","actor_type":"customer",
               "changed_by":"u-1","created_at":"2026-09-12T10:15:00Z"},
              {"id":"h-2","order_id":"o-1","from_status":"payment_pending","to_status":"confirmed",
               "actor_type":"system","created_at":"2026-09-12T10:16:00Z"},
              {"id":"h-3","order_id":"o-1","from_status":"confirmed","to_status":"packed",
               "actor_type":"seller","changed_by":"u-2","notes":"Boxed","created_at":"2026-09-12T12:00:00Z"}
            ]}
            """.trimIndent(),
        )

        val moves = dto.history.map(OrderStatusHistoryDto::toTransition)
        assertThat(moves).hasSize(3)
        assertThat(moves[0].from).isNull()
        assertThat(moves[0].to).isEqualTo(OrderStatus.PAYMENT_PENDING)
        assertThat(moves[0].actorType).isEqualTo("customer")
        assertThat(moves[1].from).isEqualTo(OrderStatus.PAYMENT_PENDING)
        assertThat(moves[1].to).isEqualTo(OrderStatus.CONFIRMED)
        assertThat(moves[1].notes).isNull()
        assertThat(moves[2].notes).isEqualTo("Boxed")
        assertThat(moves[2].at).isEqualTo("2026-09-12T12:00:00Z")

        // An empty record is an empty list, and the app derives instead.
        val empty = json.decodeFromString(
            SellerOrderHistoryDto.serializer(),
            """{"order_id":"o-2","history":[]}""",
        )
        assertThat(empty.history).isEmpty()
    }

    @Test
    fun `a detail card without a shipment or address still decodes`() {
        val dto = json.decodeFromString(
            SellerOrderCardDto.serializer(),
            """{"order":{"id":"o-2","status":"confirmed"},"items":[],"seller_subtotal_minor":0}""",
        )

        val order = dto.toSellerOrder()
        assertThat(order.shipment).isNull()
        assertThat(order.deliveryAddress).isNull()
        assertThat(order.lines).isEmpty()
        assertThat(order.status).isEqualTo(OrderStatus.CONFIRMED)
    }

    @Test
    fun `an unreadable address snapshot is null rather than a failure`() {
        // The address is one section of the screen, not the screen.
        assertThat(decodeAddressSnapshot("not base64!!")).isNull()
        assertThat(decodeAddressSnapshot(Base64.getEncoder().encodeToString("{}".toByteArray()))).isNull()
        assertThat(decodeAddressSnapshot(null)).isNull()
    }

    @Test
    fun `a routing only snapshot decodes without a name`() {
        // Post PII cutover: routing fields only. Still an address the courier
        // can use, so it must render, just without a contact.
        val encoded = Base64.getEncoder().encodeToString(
            """{"city":"Pune","state":"Maharashtra","postal_code":"411001"}""".toByteArray(),
        )
        val address = decodeAddressSnapshot(encoded)!!
        assertThat(address.contactName).isEmpty()
        assertThat(address.line1).isEmpty()
        assertThat(address.city).isEqualTo("Pune")
    }

    // Returns

    @Test
    fun `a return card decodes with and without its joined line and order`() {
        val full = json.decodeFromString(
            SellerReturnCardDto.serializer(),
            """
            {"return":{"id":"r-1","order_id":"o-1","order_item_id":"i-1","reason_code":"damaged",
                       "reason_description":"Cracked lid","status":"requested",
                       "requested_at":"2026-09-13T08:00:00Z","refund_amount":464.5},
             "order_item":{"id":"i-1","product_title":"Kettle","sku":"KET-1","quantity":1,
                           "final_price_minor":46450},
             "order":{"id":"o-1","order_number":"MS-1001"}}
            """.trimIndent(),
        ).toSellerReturn()

        assertThat(full.status).isEqualTo(ReturnStatus.REQUESTED)
        assertThat(full.status.awaitingDecision).isTrue()
        assertThat(full.orderNumber).isEqualTo("MS-1001")
        assertThat(full.itemTitle).isEqualTo("Kettle")
        assertThat(full.lineTotal).isEqualTo(Paise(46450))
        // A rupee float on the wire, paise in the app.
        assertThat(full.refundAmount).isEqualTo(Paise(46450))

        val bare = json.decodeFromString(
            SellerReturnCardDto.serializer(),
            """{"return":{"id":"r-2","order_id":"o-2","status":"rejected","rejection_reason":"Used"}}""",
        ).toSellerReturn()

        assertThat(bare.status).isEqualTo(ReturnStatus.REJECTED)
        assertThat(bare.orderNumber).isNull()
        assertThat(bare.itemTitle).isNull()
        assertThat(bare.refundAmount).isNull()
        assertThat(bare.rejectionReason).isEqualTo("Used")
    }

    // Earnings

    @Test
    fun `an earning decodes its rupee floats as exact paise`() {
        val dto = json.decodeFromString(
            SellerEarningDto.serializer(),
            """
            {"order_item_id":"i-1","order_id":"o-1","order_number":"MS-1001","product_title":"Kettle",
             "sku":"KET-1","quantity":2,"gross_amount":1299.5,"commission_amount":129.95,
             "platform_fee":20,"tds_amount":12.99,"net_amount":1136.56,
             "payment_method":"upi","status":"delivered","delivered_at":"2026-09-14T09:00:00Z"}
            """.trimIndent(),
        )

        assertThat(dto.grossAmount).isEqualTo(Paise(129950))
        assertThat(dto.commissionAmount).isEqualTo(Paise(12995))
        assertThat(dto.platformFee).isEqualTo(Paise(2000))
        assertThat(dto.tdsAmount).isEqualTo(Paise(1299))
        assertThat(dto.netAmount).isEqualTo(Paise(113656))
        assertThat(dto.paymentMethod).isEqualTo("upi")
    }

    @Test
    fun `an earning without the optional fields decodes`() {
        val dto = json.decodeFromString(
            SellerEarningDto.serializer(),
            """{"order_item_id":"i-1","gross_amount":0,"net_amount":0}""",
        )

        assertThat(dto.paymentMethod).isNull()
        assertThat(dto.deliveredAt).isNull()
        assertThat(dto.grossAmount).isEqualTo(Paise.ZERO)
        assertThat(dto.quantity).isEqualTo(1)
    }

    @Test
    fun `rupee text is scaled by decimal arithmetic, not a double`() {
        // 0.1 + 0.2 territory: these must be exact.
        assertThat(rupeeTextToPaise("1299.99")).isEqualTo(Paise(129999))
        assertThat(rupeeTextToPaise("0.29")).isEqualTo(Paise(29))
        assertThat(rupeeTextToPaise("1e3")).isEqualTo(Paise(100000))
        // A third place rounds half up, as the server's round2 does.
        assertThat(rupeeTextToPaise("1.005")).isEqualTo(Paise(101))
        assertThat(rupeeTextToPaise("")).isEqualTo(Paise.ZERO)
        assertThat(rupeeTextToPaise("n/a")).isEqualTo(Paise.ZERO)
    }
}
