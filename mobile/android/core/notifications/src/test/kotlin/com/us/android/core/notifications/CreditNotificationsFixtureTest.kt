package com.us.android.core.notifications

import com.google.common.truth.Truth.assertThat
import com.us.android.core.model.NotificationKind
import com.us.android.core.model.NotificationTarget
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.notifications.data.NotificationDto
import com.us.android.core.notifications.data.toDomain
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import org.junit.Test
import java.io.File

/**
 * Video credits' two inbox rows (2026-10-23) against notification-service's
 * golden file (`internal/http/testdata/contracts/notifications_inbox_credits.json`),
 * copied byte for byte into `src/test/resources/contracts`.
 *
 * The rows carry a `title` and NO `body` (only the push carries
 * "{role} · {title}"), so a row is drawn from its title and type. Decoded
 * with the platform `Json`: `NotificationDto` reads a row, it does not claim
 * every key (`user_id` is the recipient, which the client already knows).
 */
class CreditNotificationsFixtureTest {

    private val app = NetworkModule.provideJson()
    private val raw = File("src/test/resources/contracts/notifications_inbox_credits.json").readText()

    @Test
    fun `the rows have no body, and every key but the recipient is read`() {
        val rows = app.parseToJsonElement(raw).jsonObject.getValue("data").jsonArray.map { it.jsonObject }

        rows.forEach { row ->
            assertThat(row.keys).doesNotContain("body")
            assertThat(row.keys).containsAtLeast("type", "title", "deep_link", "entity_id", "bucket", "ts")
        }
    }

    @Test
    fun `a credit request opens the requests screen and an acceptance opens the video, titled by the server`() {
        val rows = app.decodeFromString(ApiEnvelope.serializer(ListSerializer(NotificationDto.serializer())), raw)
            .data.orEmpty().map { it.toDomain() }

        val (request, accepted) = rows
        assertThat(request.kind).isEqualTo(NotificationKind.CreditRequest)
        assertThat(request.target).isEqualTo(NotificationTarget.CreditRequests)
        assertThat(request.title).isEqualTo("Call UserB credited you in a video")
        assertThat(request.actorName).isEqualTo("Call UserB")
        assertThat(request.address.bucket).isEqualTo(202610)

        assertThat(accepted.kind).isEqualTo(NotificationKind.CreditAccepted)
        assertThat(accepted.target).isEqualTo(NotificationTarget.Video("00000000-0000-4000-8000-0000000cd002"))
        assertThat(accepted.title).isEqualTo("Someone accepted your credit")
        // No actor block on this row: the name is unknown, not "null".
        assertThat(accepted.actorName).isEmpty()
    }
}
