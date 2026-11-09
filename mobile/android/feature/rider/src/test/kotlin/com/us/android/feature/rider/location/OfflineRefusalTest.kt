package com.us.android.feature.rider.location

import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.repository.FoodError
import com.us.android.feature.rider.RiderFixtures
import org.junit.Test
import java.io.IOException

/** A refused "Go offline" keeps the rider on duty and says why (food audit M2, food server lane 9 Nov 2026). */
class OfflineRefusalTest {

    @Test
    fun `409 WORK_BUSY, from the golden, says finish the delivery`() {
        val refused = RiderFixtures.failure("delivery_availability_post_409_on_job.json")

        assertThat(offlineRefusal(refused.error)).isEqualTo(DutyNotice.OFFLINE_REFUSED_ON_JOB)
    }

    @Test
    fun `no answer or any other refusal keeps the general notice`() {
        assertThat(offlineRefusal(FoodError.Network(IOException("offline")))).isEqualTo(DutyNotice.OFFLINE_REFUSED)
        assertThat(offlineRefusal(FoodError.Unexpected(409, "WORK_REVISION_CONFLICT", "stale")))
            .isEqualTo(DutyNotice.OFFLINE_REFUSED)
        assertThat(offlineRefusal(FoodError.Unexpected(502, null, null))).isEqualTo(DutyNotice.OFFLINE_REFUSED)
    }
}
