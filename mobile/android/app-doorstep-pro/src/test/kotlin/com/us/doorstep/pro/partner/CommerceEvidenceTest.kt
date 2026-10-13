package com.us.doorstep.pro.partner

import com.google.common.truth.Truth.assertThat
import org.junit.Test

class CommerceEvidenceTest {
    @Test fun optionalIntroductionUsesWorkerDocumentRoutesOnly() {
        assertThat(introductionContract(PartnerBusiness.MOPED)).isEqualTo("v1/rider/partners/me/documents" to "introduction_video")
        assertThat(introductionContract(PartnerBusiness.FOOD_DELIVERY)).isEqualTo("v1/food/delivery/documents" to "INTRODUCTION_VIDEO")
        assertThat(introductionContract(PartnerBusiness.HOME)).isNull()
        assertThat(introductionContract(PartnerBusiness.COMMERCE_DELIVERY)).isNull()
    }
    @Test fun onlyReadyAndPassedCanBeAttached() {
        assertThat(commerceEvidenceReadiness("ready", "passed")).isEqualTo("ready")
        listOf("", "pending", "queued").forEach {
            assertThat(commerceEvidenceReadiness(it, "passed")).isEqualTo("processing")
            assertThat(commerceEvidenceReadiness("ready", it)).isEqualTo("processing")
        }
        assertThat(commerceEvidenceReadiness("failed", "passed")).isEqualTo("rejected")
        assertThat(commerceEvidenceReadiness("ready", "rejected")).isEqualTo("rejected")
    }
    @Test fun rejectsEmptyOversizedAndExecutableEvidence() {
        assertThat(commerceEvidenceError("image/jpeg", 0, false)).isNotNull()
        assertThat(commerceEvidenceError("image/svg+xml", 1024, false)).isNotNull()
        assertThat(commerceEvidenceError("text/html", 1024, true)).isNotNull()
        assertThat(commerceEvidenceError("image/jpeg", 11L*1024*1024, false)).isNotNull()
        assertThat(commerceEvidenceError("video/mp4", 51L*1024*1024, true)).isNotNull()
        assertThat(commerceEvidenceError("image/jpeg", 1024, false)).isNull()
        assertThat(commerceEvidenceError("video/mp4", 1024, true)).isNull()
    }
    @Test fun documentsDependOnVehicleAndIntroductionNeverBlocks() {
        assertThat(commerceRequiredDocuments("bicycle")).containsExactly("selfie", "identity", "bank_proof")
        assertThat(commerceRequiredDocuments("motorbike")).containsExactly("selfie", "identity", "bank_proof", "driving_licence", "vehicle_registration")
        assertThat(commerceRequiredDocuments("car")).doesNotContain("introduction")
    }
}
