package com.us.android.core.feed.data.copyright

import com.google.common.truth.Truth.assertThat
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.ui.UsReportReason
import org.junit.Test

/**
 * The report sheet's reasons and the repository's allowlist agree
 * (`ReportRepository.report` refuses a reason outside it): the copyright
 * reasons (trust-safety moderation.go:88-101) can be filed from a video's
 * or a reel's sheet.
 */
class CopyrightReportReasonsTest {

    @Test
    fun `every reason the sheet offers is one the repository files`() {
        assertThat(ReportRepository.REASONS).containsAtLeastElementsIn(UsReportReason.entries.map { it.wire })
        assertThat(ReportRepository.REASONS).containsAtLeast("copyright_mine", "copyright_media")
    }
}
