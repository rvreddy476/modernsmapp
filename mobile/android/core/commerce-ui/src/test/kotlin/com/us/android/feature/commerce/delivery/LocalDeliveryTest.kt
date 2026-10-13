package com.us.android.feature.commerce.delivery

import com.google.common.truth.Truth.assertThat
import kotlinx.serialization.json.*
import org.junit.Test
import retrofit2.Response
import retrofit2.http.GET
import retrofit2.http.PUT

class LocalDeliveryTest {
    @Test fun deliveryCodeOnlyAvailableDuringAssignedHandoverStates() {
        assertThat(canRevealDeliveryCode("assigned")).isTrue()
        assertThat(canRevealDeliveryCode("picked_up")).isTrue()
        listOf("awaiting_ready","unassigned","delivered","failed","cancelled","").forEach {
            assertThat(canRevealDeliveryCode(it)).isFalse()
        }
    }
    @Test fun assignmentDoesNotClaimShipmentPickedUp() {
        assertThat(localDeliveryStatus("assigned")).contains("awaiting pickup")
        assertThat(localDeliveryStatus("unassigned")).contains("finding")
        assertThat(localDeliveryStatus("picked_up")).contains("Picked up")
        assertThat(localDeliveryStatus("delivered")).contains("verified")
    }
    @Test fun missingDataIsAnErrorNotNoDeliveries() {
        assertThat(runCatching { localData(Response.success(buildJsonObject {})) }.isFailure).isTrue()
        assertThat(localData(Response.success(buildJsonObject {put("data",JsonNull)}))).isNull()
    }
    @Test fun codeAndLocationUseAuthenticatedOrderOwnershipEndpoints() {
        val methods=LocalDeliveryApi::class.java.methods.associateBy { it.name }
        assertThat(methods.getValue("code").getAnnotation(GET::class.java).value).isEqualTo("v1/commerce/delivery/customer/jobs/{id}/code")
        assertThat(methods.getValue("shipments").getAnnotation(GET::class.java).value).isEqualTo("v1/commerce/orders/{id}/shipments")
        assertThat(methods.getValue("location").getAnnotation(PUT::class.java).value).isEqualTo("v1/commerce/delivery/orders/{id}/location")
    }
}
