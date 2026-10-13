package com.us.doorstep.pro.partner

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.notifications.*
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.serialization.json.Json
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.time.Instant
import java.util.UUID

@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34], application = android.app.Application::class)
class PartnerOfferActionTest {
    @get:Rule val main = MainDispatcherRule()
    private val context: Context get() = ApplicationProvider.getApplicationContext()

    @Test fun practiceAcceptStopsEntireQueueAndSurvivesProcessRecreation() {
        MockWebServer().use { server ->
            server.start()
            val service=PartnerOfferDecision(Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(Json.asConverterFactory("application/json".toMediaType())).build())
            val inbox=PartnerOfferInbox()
            val notifications=NotificationPresenter(context,AppForegroundState(),DoorstepOfferAlertCoordinator(),java.util.Optional.of(com.us.android.core.notifications.data.PushApp.PARTNER),inbox)
            val responses=PartnerOfferResponses(context)
            inbox.bindAccount("practice-user")
            fun offer()=mapOf("type" to "doorstep.pro.offer.new","offer_id" to UUID.randomUUID().toString(),
                "expires_at" to Instant.now().plusSeconds(20).toString(),"test_only" to "true","decision" to "accept")
            val selected=offer()
            inbox.receive(selected)
            repeat(4){inbox.receive(offer())}
            val first=PartnerOfferActionViewModel(SavedStateHandle(selected),service,responses,notifications,inbox)
            assertThat(first.state.value.result).contains("TEST accepted")
            assertThat(inbox.offers.value).isEmpty()
            inbox.receive(offer())
            assertThat(inbox.offers.value).isEmpty()
            val recreated=PartnerOfferInbox().also {it.attachStorage(context)}
            assertThat(recreated.isBlocked(true)).isTrue()
            val second=PartnerOfferActionViewModel(SavedStateHandle(offer()),service,responses,notifications,recreated)
            assertThat(second.state.value.error).contains("already accepted")
            assertThat(server.requestCount).isEqualTo(0)
        }
    }

    @Test fun practiceAcceptDeclineAndDuplicateTapsNeverCallBackend() {
        MockWebServer().use { server ->
            server.start()
            val service = PartnerOfferDecision(Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(Json.asConverterFactory("application/json".toMediaType())).build())
            val responses = PartnerOfferResponses(context)
            val notifications = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator())
            for (category in PartnerOfferCategory.entries) for (accept in listOf(true, false)) {
                val inbox = PartnerOfferInbox()
                val data = mapOf("type" to category.wireType, "offer_id" to UUID.randomUUID().toString(),
                    "expires_at" to Instant.now().plusSeconds(20).toString(), "test_only" to "true",
                    "decision" to if(accept) "accept" else "decline")
                val first = PartnerOfferActionViewModel(SavedStateHandle(data), service, responses, notifications, inbox)
                assertThat(first.state.value.result).contains(if(accept) "TEST accepted" else "TEST declined")
                assertThat(first.state.value.result).contains("No job, payment or earnings")
                // Opposite action from another notification tap must not overwrite a recorded decision.
                val duplicate = PartnerOfferActionViewModel(SavedStateHandle(data + ("decision" to if(accept) "decline" else "accept")), service, responses, notifications, inbox)
                assertThat(duplicate.state.value.result).isEqualTo(first.state.value.result)
            }
            assertThat(server.requestCount).isEqualTo(0)
        }
    }

    @Test fun expiredPracticeRequestAndRecreatedActivityDoNotSubmit() {
        MockWebServer().use { server ->
            server.start()
            val service = PartnerOfferDecision(Retrofit.Builder().baseUrl(server.url("/"))
                .addConverterFactory(Json.asConverterFactory("application/json".toMediaType())).build())
            val responses = PartnerOfferResponses(context)
            val notifications = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator())
            val data = mapOf("type" to "doorstep.pro.offer.new", "offer_id" to UUID.randomUUID().toString(),
                "expires_at" to Instant.now().minusSeconds(1).toString(), "test_only" to "true", "decision" to "accept")
            val expired = PartnerOfferActionViewModel(SavedStateHandle(data), service, responses, notifications, PartnerOfferInbox())
            assertThat(expired.state.value.result).isNull()
            assertThat(expired.state.value.error).contains("expired")
            val restored = PartnerOfferActionViewModel(SavedStateHandle(data + mapOf("expires_at" to Instant.now().plusSeconds(20).toString(), "action_started" to true)), service, responses, notifications, PartnerOfferInbox())
            assertThat(restored.state.value.result).isNull()
            assertThat(restored.state.value.ready).isTrue()
            restored.respond(false)
            assertThat(restored.state.value.result).contains("TEST declined")
            assertThat(server.requestCount).isEqualTo(0)
        }
    }
}
