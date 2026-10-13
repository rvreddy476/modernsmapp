package com.us.android.feature.doorstep

import com.google.common.truth.Truth.assertThat
import com.us.android.core.realtime.RealtimeEvent
import com.us.android.feature.doorstep.realtime.professionalFix
import kotlinx.serialization.json.Json
import org.junit.Test
import java.time.Instant

class ProfessionalFixTest {
    @Test fun scopedBackendFrameParsesAndExpires() {
        val at = Instant.parse("2026-10-12T10:00:00Z")
        val event = RealtimeEvent.Message(null, "doorstep.booking.b1", "doorstep.booking.pro_location", Json.parseToJsonElement("""{"booking_id":"b1","lat":17.4,"lng":78.5,"eta_minutes":8,"at":"$at"}"""), at.toString())
        val fix = professionalFix("b1", event)!!
        assertThat(fix.etaMinutes).isEqualTo(8)
        assertThat(fix.isFresh(at.plusSeconds(30))).isTrue()
        assertThat(fix.isFresh(at.plusSeconds(121))).isFalse()
        assertThat(fix.isFresh(at.minusSeconds(1))).isFalse()
        assertThat(professionalFix("other", event)).isNull()
        assertThat(professionalFix("b1", event.copy(eventType = "doorstep.booking.status"))).isNull()
        assertThat(professionalFix("b1", event.copy(data = Json.parseToJsonElement("""{"booking_id":"other","lat":17.4,"lng":78.5,"at":"$at"}""")))).isNull()
    }
}
