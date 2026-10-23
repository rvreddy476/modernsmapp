package com.us.android.feature.memberships.mine

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.getOrNull
import com.us.android.core.memberships.MembershipEvents
import com.us.android.core.memberships.data.MembershipStatus
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.MyMembership
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.profile.data.ProfileRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import javax.inject.Inject

/** A membership row with the creator's name, which the server does not send. */
data class MembershipRow(
    val membership: MyMembership,
    val creatorName: String,
)

data class MyMembershipsUiState(
    val loading: Boolean = true,
    val loadError: String? = null,
    val rows: List<MembershipRow> = emptyList(),
)

/**
 * The viewer's memberships (paid memberships, 2026-10-23): live ones,
 * pending ones, and ended ones still inside the 30-day renewal window, with
 * when access ends and whether Renew is offered.
 */
@HiltViewModel
class MyMembershipsViewModel @Inject constructor(
    private val repository: MembershipsRepository,
    private val profiles: ProfileRepository,
    events: MembershipEvents,
) : ViewModel() {

    private val _state = MutableStateFlow(MyMembershipsUiState())
    val state: StateFlow<MyMembershipsUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch { load() }
        viewModelScope.launch { events.accessChanged.collect { load() } }
    }

    fun refresh() {
        _state.update { it.copy(loading = true, loadError = null) }
        viewModelScope.launch { load() }
    }

    private suspend fun load() {
        when (val result = repository.myMemberships()) {
            is AppResult.Failure -> _state.update {
                it.copy(loading = false, loadError = membershipRefusal(result.error, "Your memberships didn't load."))
            }
            is AppResult.Success -> {
                // The server names each creator; a profile is read only where its lookup failed.
                val names = creatorNames(result.data.filter { it.creatorName.isBlank() }.map { it.creatorId }.distinct())
                _state.update {
                    it.copy(
                        loading = false,
                        loadError = null,
                        rows = result.data.map { m -> MembershipRow(m, m.creatorName.ifBlank { names[m.creatorId].orEmpty() }) },
                    )
                }
            }
        }
    }

    private suspend fun creatorNames(ids: List<String>): Map<String, String> = coroutineScope {
        ids.map { id ->
            async { id to profiles.getProfile(id).getOrNull()?.let { it.displayName.ifBlank { it.username } } }
        }
            .awaitAll()
            .mapNotNull { (id, name) -> name?.takeIf { it.isNotBlank() }?.let { id to it } }
            .toMap()
    }
}

/** "23 Nov 2026" in the viewer's zone, or the raw text when it is not RFC 3339. */
internal fun membershipDate(rfc3339: String?, zone: ZoneId = ZoneId.systemDefault()): String {
    val raw = rfc3339?.trim().orEmpty()
    if (raw.isEmpty()) return ""
    return runCatching {
        OffsetDateTime.parse(
            raw
        ).atZoneSameInstant(zone).format(DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH))
    }.getOrDefault(raw)
}

/** The row's one line about time: when access ends, or when it ended. */
internal fun MyMembership.timeLine(zone: ZoneId = ZoneId.systemDefault()): String {
    val until = membershipDate(accessUntil, zone)
    val end = membershipDate(currentPeriodEnd, zone)
    return when {
        hasAccess && status == MembershipStatus.CancelledAtPeriodEnd ->
            "Cancelled · access until $until"
        hasAccess && status == MembershipStatus.Grace -> "Renew by $until to keep access"
        hasAccess -> "Access until $until · renew by then"
        status == MembershipStatus.PendingPayment -> "Waiting for your payment"
        end.isNotEmpty() -> "Ended $end"
        else -> status.label
    }
}
