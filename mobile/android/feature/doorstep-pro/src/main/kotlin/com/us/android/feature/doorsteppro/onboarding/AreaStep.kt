package com.us.android.feature.doorsteppro.onboarding

import android.Manifest
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.SliderDefaults
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.core.app.ActivityCompat
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsSecondaryButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.domain.AreaRules
import com.us.android.feature.doorsteppro.location.Coordinates
import com.us.android.feature.doorsteppro.location.CurrentLocationSource
import com.us.android.feature.doorsteppro.location.LocationEffect
import com.us.android.feature.doorsteppro.location.LocationPermissionFlow
import com.us.android.feature.doorsteppro.location.LocationStep
import com.us.android.feature.doorsteppro.location.PlaceLookup
import com.us.android.feature.doorsteppro.store.OnboardingMemory
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LabeledValue
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.findActivity
import com.us.android.feature.doorsteppro.ui.listPadding
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.Locale
import javax.inject.Inject

/** The home point and the zone doorstep-service placed it in. */
data class HomePoint(val coordinates: Coordinates, val zoneId: String, val zoneName: String)

data class AreaUiState(
    val location: LocationStep = LocationStep.Idle,
    val query: String = "",
    val resolving: Boolean = false,
    val home: HomePoint? = null,
    /** The point is outside every active zone: Doorstep doesn't serve it yet. */
    val outside: Boolean = false,
    val radiusKm: Int = AreaRules.DEFAULT_KM,
    val saving: Boolean = false,
    val saved: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Where the professional works: a home point (current location after an
 * in-app rationale, or a typed area), the zone doorstep-service places it in
 * (`POST /v1/doorstep/serviceability`) and a radius of 1–15 km →
 * `PUT /pro/me/area`. No background location: one fix, now, in the foreground.
 */
@HiltViewModel
class AreaViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val location: CurrentLocationSource,
    private val places: PlaceLookup,
    private val memory: OnboardingMemory,
) : ViewModel() {

    private val flow = LocationPermissionFlow()

    private val _state = MutableStateFlow(AreaUiState(radiusKm = memory.radiusKm() ?: AreaRules.DEFAULT_KM))
    val state: StateFlow<AreaUiState> = _state.asStateFlow()

    private val _requestPermission = MutableSharedFlow<Unit>(extraBufferCapacity = 1, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val requestPermission: SharedFlow<Unit> = _requestPermission.asSharedFlow()

    fun useCurrentLocation() = perform(flow.onUseCurrentLocation(location.hasPermission()))

    fun onRationaleAccepted() = perform(flow.onRationaleAccepted())

    fun onRationaleDismissed() {
        flow.onRationaleDismissed()
        publish()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) = perform(flow.onPermissionResult(granted, canAskAgain))

    fun onQuery(text: String) = _state.update { it.copy(query = text.take(MAX_QUERY)) }

    fun findTypedArea() {
        val query = _state.value.query.trim()
        if (query.isEmpty()) return
        _state.update { it.copy(resolving = true) }
        viewModelScope.launch {
            val point = places.forward("$query, Hyderabad")
            if (point == null) {
                _state.update { it.copy(resolving = false, message = errorMessage("Couldn't find that place. Try a nearby landmark or area name.")) }
            } else {
                resolveZone(point)
            }
        }
    }

    fun onRadius(km: Int) = _state.update { it.copy(radiusKm = AreaRules.clampKm(km)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val current = _state.value
        val home = current.home
        if (home == null) {
            _state.update { it.copy(message = errorMessage("Set your home point first.")) }
            return
        }
        if (current.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            val result = repository.saveArea(
                zoneIds = listOf(home.zoneId),
                homeLat = home.coordinates.latitude,
                homeLng = home.coordinates.longitude,
                radiusM = AreaRules.radiusMeters(current.radiusKm),
            )
            when (result) {
                is ProResult.Success -> {
                    memory.saveRadiusKm(AreaRules.kmOf(result.value.radiusM))
                    _state.update { it.copy(saving = false, saved = true) }
                }
                is ProResult.Failure -> _state.update { it.copy(saving = false, message = result.error.asMessage()) }
            }
        }
    }

    private fun perform(effect: LocationEffect) {
        publish()
        when (effect) {
            LocationEffect.None -> Unit
            LocationEffect.RequestPermission -> _requestPermission.tryEmit(Unit)
            LocationEffect.FetchLocation -> viewModelScope.launch {
                val fix = location.current()
                flow.onLocationResult(fix)
                publish()
                if (fix != null) resolveZone(fix)
            }
        }
    }

    private suspend fun resolveZone(point: Coordinates) {
        _state.update { it.copy(resolving = true, outside = false) }
        when (val result = repository.serviceability(point.latitude, point.longitude)) {
            is ProResult.Success -> {
                val zone = result.value.zone
                if (result.value.serviceable && zone != null) {
                    _state.update { it.copy(resolving = false, home = HomePoint(point, zone.id, zone.name)) }
                } else {
                    _state.update { it.copy(resolving = false, home = null, outside = true) }
                }
            }
            is ProResult.Failure -> _state.update { it.copy(resolving = false, message = result.error.asMessage()) }
        }
    }

    private fun publish() = _state.update { it.copy(location = flow.step) }

    private companion object {
        const val MAX_QUERY = 120
    }
}

@Composable
fun AreaStepScreen(onBack: () -> Unit, viewModel: AreaViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    val context = LocalContext.current
    LaunchedEffect(state.saved) { if (state.saved) onBack() }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { grants ->
        val activity = context.findActivity()
        val canAskAgain = activity != null && ActivityCompat.shouldShowRequestPermissionRationale(activity, Manifest.permission.ACCESS_FINE_LOCATION)
        viewModel.onPermissionResult(grants.values.any { it }, canAskAgain)
    }
    LaunchedEffect(viewModel) {
        viewModel.requestPermission.collect {
            permission.launch(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION, Manifest.permission.ACCESS_COARSE_LOCATION))
        }
    }
    if (state.location == LocationStep.ExplainingPermission) {
        AlertDialog(
            onDismissRequest = viewModel::onRationaleDismissed,
            title = { Text("Use your location once") },
            text = {
                Text(
                    "Doorstep Pro reads your location one time to set your home point — where your travel radius starts. " +
                        "It is not tracked. You can type your area instead.",
                )
            },
            confirmButton = { TextButton(onClick = viewModel::onRationaleAccepted) { Text("Continue") } },
            dismissButton = { TextButton(onClick = viewModel::onRationaleDismissed) { Text("Type it instead") } },
        )
    }
    ProScreen(
        title = "Where you work",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save area", onClick = viewModel::save, loading = state.saving, enabled = state.home != null) },
    ) { padding ->
        Column(
            modifier = Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(listPadding(padding)),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            ProCard {
                CardHeading("Your home point", "Jobs are offered within your travel radius of this point.")
                UsSecondaryButton(
                    text = if (state.location == LocationStep.Locating) "Finding you…" else "Use my current location",
                    onClick = viewModel::useCurrentLocation,
                    enabled = state.location != LocationStep.Locating && !state.resolving,
                    modifier = Modifier.fillMaxWidth(),
                )
                when (val step = state.location) {
                    is LocationStep.Denied -> InfoNote(
                        if (step.canAskAgain) "Location wasn't allowed. Type your area below." else "Location is off for Doorstep Pro. Type your area below.",
                        tone = Tone.Warning,
                    )
                    LocationStep.Unavailable -> InfoNote("Couldn't get a fix. Type your area below.", tone = Tone.Warning)
                    else -> Unit
                }
                UsTextField(value = state.query, onValueChange = viewModel::onQuery, label = "Or type your area", placeholder = "e.g. Kondapur")
                UsSecondaryButton(
                    text = if (state.resolving) "Looking…" else "Find this area",
                    onClick = viewModel::findTypedArea,
                    enabled = !state.resolving && state.query.isNotBlank(),
                    modifier = Modifier.fillMaxWidth(),
                )
                state.home?.let { home ->
                    LabeledValue("Zone", home.zoneName)
                    LabeledValue(
                        "Point",
                        String.format(Locale.ENGLISH, "%.4f, %.4f", home.coordinates.latitude, home.coordinates.longitude),
                    )
                }
                if (state.outside) InfoNote("Doorstep doesn't serve that point yet. Pick a place inside Hyderabad's service area.", tone = Tone.Danger)
            }
            ProCard {
                CardHeading("How far you'll travel", "${state.radiusKm} km from your home point")
                Slider(
                    value = state.radiusKm.toFloat(),
                    onValueChange = { viewModel.onRadius(it.toInt()) },
                    valueRange = AreaRules.MIN_KM.toFloat()..AreaRules.MAX_KM.toFloat(),
                    steps = AreaRules.MAX_KM - AreaRules.MIN_KM - 1,
                    colors = SliderDefaults.colors(
                        thumbColor = UsTheme.extended.accentSolid,
                        activeTrackColor = UsTheme.extended.accentSolid,
                        inactiveTrackColor = UsTheme.extended.borderSubtle,
                    ),
                )
                Text("1 km to 15 km", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
    }
}
