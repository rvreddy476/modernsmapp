package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.engagement.data.ReportOutcome
import com.us.android.core.engagement.data.ReportRepository
import com.us.android.core.model.Profile
import com.us.android.core.ui.UsPostReportState
import com.us.android.core.ui.UsReportReason
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Reports the account a profile shows (2026-10-14, audit T-03): trust-safety's
 * `POST /v1/reports` with entity type `user` and the PROFILE's user id, through
 * the same [ReportRepository] and the same reason sheet a post's report uses.
 *
 * Its own ViewModel rather than another responsibility on `ProfileViewModel`,
 * like [StartChatViewModel]: the report's progress belongs to the sheet that
 * is open, not to the profile loader, and a profile refresh must not reset it.
 *
 * The viewer's own profile is refused here as well as in the menu
 * ([profileMenuRows]) — reporting yourself is not a thing the app offers, and
 * a second guard costs one line.
 */
@HiltViewModel
class ProfileReportViewModel @Inject constructor(
    private val reports: ReportRepository,
) : ViewModel() {

    private val _report = MutableStateFlow<UsPostReportState>(UsPostReportState.Idle)

    /** The report step's progress for the profile the sheet is open on. */
    val report: StateFlow<UsPostReportState> = _report.asStateFlow()

    /** The sheet opened: nothing from an earlier report carries over. */
    fun reset() {
        _report.value = UsPostReportState.Idle
    }

    fun report(profile: Profile, reason: UsReportReason, details: String) {
        if (profile.isOwnProfile || profile.userId.isBlank()) return
        if (_report.value == UsPostReportState.Sending) return
        _report.value = UsPostReportState.Sending
        viewModelScope.launch {
            _report.value = when (reports.reportUser(profile.userId, reason.wire, details)) {
                ReportOutcome.Filed -> UsPostReportState.Sent
                ReportOutcome.AlreadyReported -> UsPostReportState.AlreadyReported
                is ReportOutcome.Failed -> UsPostReportState.Failed
            }
        }
    }
}
