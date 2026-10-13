package com.us.android.feature.doorsteppro

import com.google.common.truth.Truth.assertThat
import com.us.android.feature.doorsteppro.location.Coordinates
import com.us.android.feature.doorsteppro.onboarding.HomePoint
import org.junit.Test

class WorkingAreaDraftTest {
    @Test
    fun `uncovered location saves without inventing a zone`() {
        val point = Coordinates(17.4, 78.6)
        val home = HomePoint(point, null, "Location saved for setup")
        assertThat(home.coordinates).isEqualTo(point)
        assertThat(home.zoneIds).isEmpty()
    }

    @Test
    fun `covered location retains the server zone`() {
        val home = HomePoint(Coordinates(17.4, 78.6), "server-zone", "Working area")
        assertThat(home.zoneIds).containsExactly("server-zone")
    }
}
