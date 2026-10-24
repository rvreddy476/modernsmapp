package com.us.android.feature.notifications.ui

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.model.Notification
import com.us.android.core.model.NotificationIcon
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import org.junit.Test

/**
 * The Activity row against notification-service's `title` / `icon` fields
 * (2026-10-18): which sentence a row shows, and what its avatar slot draws.
 */
class ActivityRowContractTest {

    private fun row(
        type: String,
        actorName: String = "Asha",
        actorId: String = "u1",
        title: String? = null,
        icon: NotificationIcon? = null,
    ) = Notification(
        id = "n1",
        bucket = 1,
        ts = "ts",
        kind = NotificationKind.fromWire(type),
        actorUserId = actorId,
        actorName = actorName,
        entityType = "",
        entityId = "",
        target = NotificationTarget.None,
        isRead = false,
        createdAt = "2026-10-18T10:00:00Z",
        title = title,
        icon = icon,
    )

    // ── Sentences ───────────────────────────────────────────────────────

    @Test
    fun `an unknown type uses the server title`() {
        assertThat(row("dating.match.formed", title = "You and Asha matched").describe())
            .isEqualTo("You and Asha matched")
    }

    @Test
    fun `an unknown type without a title falls to the customer line, then the generic one`() {
        assertThat(row("order_shipped", actorName = "", actorId = "").describe()).isEqualTo("Shopping order: on the way")
        assertThat(row("live_gift").describe()).isEqualTo("You have a new notification")
    }

    @Test
    fun `the server title beats the customer line for a customer type`() {
        assertThat(row("order_shipped", title = "Order #1042 has shipped").describe())
            .isEqualTo("Order #1042 has shipped")
    }

    @Test
    fun `a known kind keeps its own sentence whatever the server says`() {
        assertThat(row("reaction", title = "Asha liked your photo").describe()).isEqualTo("Asha reacted to your post")
        assertThat(row("follow", title = "Asha followed you").describe()).isEqualTo("Asha started following you")
    }

    @Test
    fun `every stored type the inbox sees resolves to a real sentence given a server title`() {
        val types = listOf(
            "dm", "friend_request", "friend_accepted", "incoming_call", "incoming_video_call",
            "comment_reaction", "dating.match.expired", "order_confirmed", "order_shipped",
            "order_delivered", "order_cancelled", "order_refunded", "order_payment_failed",
            "commerce_invoice_issued",
        )
        for (type in types) {
            val sentence = row(type, title = "Something specific happened").describe()
            assertThat(sentence).isNotEqualTo("You have a new notification")
            assertThat(sentence).isNotEmpty()
        }
    }

    @Test
    fun `incoming call rows say Call from, never Missed, the video one as video`() {
        // Written on every invite, answered or not (server title agrees, 18 Oct).
        assertThat(row("incoming_call").describe()).isEqualTo("Call from Asha")
        assertThat(row("incoming_video_call").describe()).isEqualTo("Video call from Asha")
        assertThat(row("incoming_video_call", actorName = "").describe()).isEqualTo("Video call")
    }

    @Test
    fun `the social types the parse table already knew keep their own sentences`() {
        assertThat(row("dm").describe()).isEqualTo("Asha sent you a message")
        // Connect is gone (24 Oct 2026): an old request row says so, the web's words.
        assertThat(row("friend_request").describe()).isEqualTo("Connections have ended. Follow or message them instead.")
        assertThat(row("friend_accepted").describe()).isEqualTo("Asha accepted your connection request")
        assertThat(row("comment_reaction").describe()).isEqualTo("Asha reacted to your comment")
    }

    @Test
    fun `an expired match names the other person`() {
        assertThat(row("dating.match.expired").describe()).isEqualTo("Your match with Asha expired")
        assertThat(row("dating.match.expired", actorName = "").describe()).isEqualTo("A match expired")
    }

    @Test
    fun `the new invoice and failed-payment types are not generic without a title`() {
        assertThat(row("commerce_invoice_issued", actorName = "", actorId = "").describe())
            .isEqualTo("Shopping order: invoice ready")
        assertThat(row("order_payment_failed", actorName = "", actorId = "").describe())
            .isEqualTo("Shopping order: payment failed")
    }

    // ── Avatar slot ─────────────────────────────────────────────────────

    @Test
    fun `a row with an actor draws the actor`() {
        assertThat(row("reaction", icon = NotificationIcon.Social).rowAvatar()).isEqualTo(RowAvatar.Actor)
    }

    @Test
    fun `a row with no actor draws the server's category`() {
        val nil = "00000000-0000-0000-0000-000000000000"
        assertThat(row("order_shipped", actorName = "", actorId = nil, icon = NotificationIcon.Order).rowAvatar())
            .isEqualTo(RowAvatar.Glyph(NotificationIcon.Order))
        assertThat(row("ride.assigned", actorName = "", actorId = "", icon = NotificationIcon.Ride).rowAvatar())
            .isEqualTo(RowAvatar.Glyph(NotificationIcon.Ride))
        // An actor id whose name never hydrated has nothing to draw either.
        assertThat(row("qa.answer.created", actorName = "", icon = NotificationIcon.Qa).rowAvatar())
            .isEqualTo(RowAvatar.Glyph(NotificationIcon.Qa))
    }

    @Test
    fun `without a server icon the category is inferred from the type`() {
        fun inferred(type: String) = row(type, actorName = "", actorId = "").category()
        assertThat(inferred("order_shipped")).isEqualTo(NotificationIcon.Order)
        assertThat(inferred("commerce_invoice_issued")).isEqualTo(NotificationIcon.Order)
        assertThat(inferred("food_order_status")).isEqualTo(NotificationIcon.Food)
        assertThat(inferred("doorstep.booking.assigned")).isEqualTo(NotificationIcon.HomeService)
        assertThat(inferred("ride.completed")).isEqualTo(NotificationIcon.Ride)
        assertThat(inferred("dating.match.formed")).isEqualTo(NotificationIcon.Dating)
        assertThat(inferred("dating.match.expired")).isEqualTo(NotificationIcon.Dating)
        assertThat(inferred("qa.answer.created")).isEqualTo(NotificationIcon.Qa)
        assertThat(inferred("missed_call")).isEqualTo(NotificationIcon.Call)
        assertThat(inferred("dm")).isEqualTo(NotificationIcon.Message)
        assertThat(inferred("creator_went_live")).isEqualTo(NotificationIcon.Live)
        assertThat(inferred("reaction")).isEqualTo(NotificationIcon.Social)
        assertThat(inferred("something_new")).isEqualTo(NotificationIcon.System)
    }

    @Test
    fun `each category picks its own glyph`() {
        val expected = mapOf(
            NotificationIcon.Order to UsIcons.ShoppingBag,
            NotificationIcon.Food to UsIcons.Utensils,
            NotificationIcon.HomeService to UsIcons.Wrench,
            NotificationIcon.Ride to UsIcons.Bike,
            NotificationIcon.Dating to UsIcons.HeartOutline,
            NotificationIcon.Call to UsIcons.Phone,
            NotificationIcon.Live to UsIcons.Radio,
            NotificationIcon.Message to UsIcons.Comment,
            NotificationIcon.Community to UsIcons.Friends,
            NotificationIcon.Qa to UsIcons.CircleHelp,
            NotificationIcon.Account to UsIcons.Profile,
            NotificationIcon.System to UsIcons.Notifications,
            NotificationIcon.Social to UsIcons.Notifications,
        )
        assertThat(expected.keys).containsExactlyElementsIn(NotificationIcon.entries)
        for ((category, glyph) in expected) {
            assertThat(category.glyph()).isSameInstanceAs(glyph)
        }
    }
}
