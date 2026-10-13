package com.us.doorstep.pro.partner

import android.content.Context
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.auth.AuthRepository
import com.us.android.core.notifications.data.*
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.*
import kotlinx.coroutines.flow.*
import kotlinx.serialization.json.*
import retrofit2.Retrofit
import retrofit2.Response
import retrofit2.http.GET
import retrofit2.http.Url
import retrofit2.http.Headers
import javax.inject.Inject

internal interface PartnerWorkspaceApi {
    @Headers("Cache-Control: no-cache")
    @GET suspend fun profile(@Url path: String): Response<JsonObject>
}

/** Shared by the workspace and wire-contract tests; no profile or permission is invented. */
internal suspend fun loadBusinessSummary(api: PartnerWorkspaceApi, role: PartnerBusiness): BusinessSummary {
    val endpoint = role.endpoint ?: return BusinessSummary(role, BusinessStatus.UNAVAILABLE)
    return try {
        val response = api.profile(endpoint)
        if (response.isSuccessful) businessSummary(role, response.body()?.get("data"))
        else {
            val error = response.errorBody()?.string()?.let { runCatching { Json.parseToJsonElement(it).jsonObject }.getOrNull() }
            businessFailureSummary(role, response.code(), error?.get("error") as? JsonObject)
        }
    } catch (cancel: CancellationException) {
        throw cancel
    } catch (_: Exception) {
        BusinessSummary(role, BusinessStatus.UNKNOWN)
    }
}

data class WorkspaceState(
    val summaries: List<BusinessSummary> = PartnerBusiness.entries.map {
        BusinessSummary(it, if (it.available) BusinessStatus.LOADING else BusinessStatus.UNAVAILABLE)
    },
    val selected: Set<PartnerBusiness> = emptySet(),
    val refreshing: Boolean = true,
    val alerts: List<NotificationDto> = emptyList(),
    val alertsLoading: Boolean = false,
    val alertsError: Boolean = false,
    val workSession: PartnerWorkSession? = null,
    val workLoading: Boolean = true,
    val workError: Boolean = false,
)

@HiltViewModel
class PartnerWorkspaceViewModel @Inject constructor(
    retrofit: Retrofit,
    private val auth: AuthRepository,
    @ApplicationContext context: Context,
    private val proDuty: com.us.android.feature.doorsteppro.location.ProDuty,
    private val riderDuty: com.us.android.feature.rider.location.RiderDuty,
    private val captainDuty: com.us.android.feature.mopedu.captain.location.CaptainDuty,
    private val notificationRouter: PartnerNotificationRouter,
    private val notificationUpdates: com.us.android.core.notifications.NotificationUpdates,
) : ViewModel() {
    private val api = retrofit.create(PartnerWorkspaceApi::class.java)
    private val notifications = retrofit.create(NotificationsApi::class.java)
    private val preferences = context.getSharedPreferences("partner_workspace", Context.MODE_PRIVATE)
    private var userId: String? = null
    private var refreshJob: Job? = null
    private var alertsJob: Job? = null
    private var visibleJob: Job? = null
    private var workJob: Job? = null
    private val mutable = MutableStateFlow(WorkspaceState())
    val state = mutable.asStateFlow()

    fun setVisible(visible: Boolean) {
        visibleJob?.cancel()
        visibleJob = if (visible) viewModelScope.launch {
            launch {
                notificationUpdates.events.filter { PartnerBusiness.forNotification(it) != null }.collect {
                    loadAlerts(); refresh()
                }
            }
            // Covers missed/disabled push and network recovery, only while the app is visible.
            while (isActive) { loadAlerts(); loadWork(); delay(15_000) }
        } else null
    }

    fun openAlert(alert: NotificationDto): PartnerBusiness? {
        val business = notificationRouter.open(alert.type, alert.entityId, alert.deepLink) ?: return null
        read(alert)
        return business
    }

    fun initialise(id: String) {
        if (userId == id) return
        refreshJob?.cancel(); alertsJob?.cancel()
        userId = id
        val saved = preferences.getStringSet("businesses:$id", emptySet()).orEmpty()
        val migrated = selectedBusinesses(saved)
        // Permanently drop retired Fish/merchant selections; never reinterpret them.
        preferences.edit().putStringSet("businesses:$id", migrated.map { it.name }.toSet()).apply()
        mutable.value = WorkspaceState(selected = migrated)
        refresh()
    }

    fun refresh() {
        loadWork()
        if (refreshJob?.isActive == true) return
        refreshJob = viewModelScope.launch {
            mutable.update { it.copy(refreshing = true) }
            coroutineScope {
                PartnerBusiness.entries.map { role ->
                    async {
                        val summary = loadBusinessSummary(api, role)
                        ensureActive()
                        // Render each business independently, even if another service is slow.
                        mutable.update { previous ->
                            previous.copy(summaries = previous.summaries.map { old ->
                                if (old.business != role) old
                                else mergeBusinessRefresh(old, summary)
                            })
                        }
                    }
                }.awaitAll()
            }
            mutable.update { it.copy(refreshing = false) }
        }
    }

    fun loadWork() {
        if (workJob?.isActive == true) return
        workJob = viewModelScope.launch {
            try {
                val response = api.profile("v1/partner/work-session")
                if (!response.isSuccessful) error("Work status unavailable")
                val session = parseWorkSession(response.body()?.get("data"))
                mutable.update { it.copy(workSession = session, workLoading = false, workError = false) }
            } catch (cancel: CancellationException) { throw cancel }
            catch (_: Exception) { mutable.update { it.copy(workLoading = false, workError = true) } }
        }
    }

    fun openWork(reservation: PartnerReservation): PartnerBusiness {
        if (reservation.state == "offered") notificationRouter.openRequests(reservation.business)
        else notificationRouter.openWork(reservation.business, reservation.jobId)
        return reservation.business
    }

    fun saveBusinesses(roles: Set<PartnerBusiness>) {
        val id = userId ?: return
        val supported = roles.filter { it.available }.toSet()
        preferences.edit().putStringSet("businesses:$id", supported.map { it.name }.toSet()).apply()
        mutable.update { it.copy(selected = supported) }
    }

    fun syncHomeStatus(id: String, name: String, status: String) {
        val summary = businessSummary(PartnerBusiness.HOME, buildJsonObject {
            put("id", id); put("display_name", name); put("status", status)
        })
        mutable.update { it.copy(summaries = it.summaries.map { row -> if (row.business == PartnerBusiness.HOME) summary else row }) }
    }

    fun loadAlerts() {
        if (mutable.value.alertsLoading) return
        alertsJob = viewModelScope.launch {
            mutable.update { it.copy(alertsLoading = true, alertsError = false) }
            try {
                val result = notifications.list(100)
                mutable.update { it.copy(alerts = result.data.orEmpty().filter { n -> PartnerBusiness.forNotification(n.type) != null }, alertsLoading = false) }
            } catch (cancel: CancellationException) { throw cancel }
            catch (_: Exception) { mutable.update { it.copy(alertsLoading = false, alertsError = true) } }
        }
    }

    fun read(alert: NotificationDto) {
        viewModelScope.launch {
            try {
                notifications.markRead(MarkReadRequest(alert.bucket, alert.ts))
                mutable.update { it.copy(alerts = it.alerts.map { row -> if (row.notificationId == alert.notificationId) row.copy(isRead = true) else row }) }
            } catch (cancel: CancellationException) { throw cancel }
            catch (_: Exception) { /* Opening work is still allowed when read acknowledgement fails. */ }
        }
    }

    fun signOut() {
        proDuty.requestStop(com.us.android.feature.doorsteppro.location.OfflineReason.SIGNED_OUT)
        riderDuty.requestStop(com.us.android.feature.rider.location.OfflineReason.SIGNED_OUT)
        captainDuty.requestStop(com.us.android.feature.mopedu.captain.location.OfflineReason.SIGNED_OUT)
        viewModelScope.launch { auth.logout() }
    }
}
