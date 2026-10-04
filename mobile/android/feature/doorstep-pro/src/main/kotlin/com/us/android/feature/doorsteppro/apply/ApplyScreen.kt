package com.us.android.feature.doorsteppro.apply

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProCodes
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.code
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.CardStack
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class ApplyUiState(
    val name: String = "",
    val nameError: String? = null,
    val submitting: Boolean = false,
    val done: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * `POST /pro/apply {display_name, city_code}`. No gender field: gender comes
 * from DigiLocker Aadhaar only, and the server refuses the key outright.
 * Already applied (409 DOORSTEP_PRO_EXISTS) counts as done — the gate reloads.
 */
@HiltViewModel
class ApplyViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ApplyUiState())
    val state: StateFlow<ApplyUiState> = _state.asStateFlow()

    fun onName(value: String) = _state.update { it.copy(name = value.take(MAX_NAME), nameError = null) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun submit() {
        val name = _state.value.name.trim()
        if (name.length < MIN_NAME) {
            _state.update { it.copy(nameError = "Enter your name as customers should see it") }
            return
        }
        if (_state.value.submitting) return
        _state.update { it.copy(submitting = true) }
        viewModelScope.launch {
            when (val result = repository.apply(displayName = name, cityCode = PILOT_CITY)) {
                is ProResult.Success -> _state.update { it.copy(submitting = false, done = true) }
                is ProResult.Failure -> if (result.error.code == ProCodes.PRO_EXISTS) {
                    _state.update { it.copy(submitting = false, done = true) }
                } else {
                    _state.update { it.copy(submitting = false, message = result.error.asMessage()) }
                }
            }
        }
    }

    companion object {
        /** The pilot city (GST state 36). */
        const val PILOT_CITY = "HYD"
        private const val MIN_NAME = 2
        private const val MAX_NAME = 80
    }
}

@Composable
fun ApplyScreen(onBack: () -> Unit, onApplied: () -> Unit, viewModel: ApplyViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LaunchedEffect(state.done) { if (state.done) onApplied() }
    ProScreen(
        title = "Work with Doorstep",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Start my application", onClick = viewModel::submit, loading = state.submitting) },
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .verticalScroll(rememberScrollState())
                .padding(listPadding(padding)),
        ) {
            CardStack {
                ProCard {
                    CardHeading(
                        "Home services in Hyderabad",
                        "Cleaning, AC and appliance repair, electrical, plumbing, carpentry, painting, pest control and salon at home.",
                    )
                    UsTextField(
                        value = state.name,
                        onValueChange = viewModel::onName,
                        label = "Your name",
                        placeholder = "As on your Aadhaar",
                        errorText = state.nameError,
                    )
                    InfoNote("City: Hyderabad. Doorstep Pro is in a pilot there.")
                }
                InfoNote(
                    "Next you'll verify Aadhaar with DigiLocker, take a selfie, choose your skills, area and hours, " +
                        "add your bank account and upload a police clearance certificate. Doorstep reviews it, then you can take jobs.",
                    modifier = Modifier.padding(horizontal = UsTheme.spacing.xs),
                )
            }
        }
    }
}
