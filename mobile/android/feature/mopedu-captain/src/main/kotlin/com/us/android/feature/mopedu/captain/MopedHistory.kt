package com.us.android.feature.mopedu.captain

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.compose.LifecycleResumeEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.*
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.mobility.model.RideBooking
import com.us.android.feature.mopedu.captain.data.*
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.*
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json
import javax.inject.Inject

data class MopedHistoryState(val loading: Boolean = true, val busy: Boolean = false, val rides: List<RideBooking> = emptyList(), val message: String? = null)
@HiltViewModel
class MopedHistoryViewModel @Inject constructor(private val api: MopeduCaptainApi, private val json: Json) : ViewModel() {
    private val mutable = MutableStateFlow(MopedHistoryState())
    val state = mutable.asStateFlow()
    fun refresh() {
        viewModelScope.launch {
            mutable.update { it.copy(loading = true) }
            when (val result = captainCall(json) { api.rideHistory() }) {
                is CaptainResult.Success -> mutable.update { it.copy(loading = false, rides = result.value.map(CaptainRideDto::toBooking)) }
                is CaptainResult.Failure -> mutable.update { it.copy(loading = false, message = "Couldn't refresh your rides. Please try again; previously loaded rides remain below.") }
            }
        }
    }
    fun support(id: String, category: String, description: String) {
        if (mutable.value.busy) return
        mutable.update { it.copy(busy = true, message = null) }
        viewModelScope.launch {
            val result = captainCall(json) { api.support(id, CaptainSupportRequestDto(category, description.trim())) }
            mutable.update { it.copy(busy = false, message = when(result) {
                is CaptainResult.Success -> "Support request sent. Reference ${result.value.id.take(8)}."
                is CaptainResult.Failure -> "Couldn't send the support request. Check your connection and try again."
            }) }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun MopedHistoryScreen(onBack: () -> Unit, viewModel: MopedHistoryViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    var selected by remember { mutableStateOf<RideBooking?>(null) }
    LifecycleResumeEffect(viewModel) { viewModel.refresh(); onPauseOrDispose {} }
    LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(UsTheme.spacing.pageHorizontal), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
        item { TextButton(onBack) { Text("Back to Moped") }; Text("Ride history & support", style = MaterialTheme.typography.titleLarge) }
        item { if (state.loading) LinearProgressIndicator(Modifier.fillMaxWidth()); state.message?.let { Text(it) }; TextButton(viewModel::refresh, enabled = !state.loading) { Text("Refresh") } }
        if (state.rides.isEmpty() && !state.loading) item { Text("Your rides will appear here after a request is accepted.") }
        items(state.rides, key = { it.id }) { ride -> UsSurfaceCard(Modifier.fillMaxWidth()) {
            Text(ride.status.name.lowercase().replace('_', ' ').replaceFirstChar(Char::uppercase), style = MaterialTheme.typography.titleMedium)
            Text(ride.pickup.address.ifBlank { "Pickup" }); Text("To ${ride.drop.address.ifBlank { "Destination" }}")
            Text((ride.finalFare ?: ride.estimatedFare).formattedINR, style = MaterialTheme.typography.titleMedium)
            Text("Ride ${ride.id.take(8)}", style = MaterialTheme.typography.bodySmall)
            UsSecondaryButton("Ask for help", { selected = ride }, enabled = !state.busy)
        } }
    }
    selected?.let { ride ->
        var reason by rememberSaveable(ride.id) { mutableStateOf("") }
        var category by rememberSaveable(ride.id) { mutableStateOf("other") }
        AlertDialog(onDismissRequest = { selected = null }, title = { Text("Help with this ride") }, text = {
            Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                Text("For immediate danger, contact emergency services. This form is for ride support.")
                FlowRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs)) {
                    listOf("other" to "Other", "fare_dispute" to "Fare", "safety" to "Safety", "vehicle_condition" to "Vehicle", "route_deviation" to "Route").forEach { (key, label) ->
                        FilterChip(category == key, { category = key }, label = { Text(label) })
                    }
                }
                OutlinedTextField(reason, { reason = it.take(2000) }, label = { Text("What happened?") }, modifier = Modifier.fillMaxWidth(), minLines = 3)
            }
        }, confirmButton = { TextButton({ viewModel.support(ride.id, category, reason); selected = null }, enabled = reason.trim().length >= 10) { Text("Send request") } }, dismissButton = { TextButton({ selected = null }) { Text("Cancel") } })
    }
}
