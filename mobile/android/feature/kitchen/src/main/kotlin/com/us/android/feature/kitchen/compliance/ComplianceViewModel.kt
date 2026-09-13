package com.us.android.feature.kitchen.compliance

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.ComplianceDto
import com.us.android.core.food.network.ComplianceRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.kyc.Gstin
import com.us.android.feature.kitchen.kyc.GstinCheck
import com.us.android.feature.kitchen.kyc.Pan
import com.us.android.feature.kitchen.kyc.PanCheck
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.queue.KitchenClock
import com.us.android.feature.kitchen.ui.IndiaTime
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.LocalDate
import javax.inject.Inject

/**
 * The restaurant tax categories food-service accepts (shared/gst Category,
 * supplier RESTAURANT). The SERVER decides from the category whether a GSTIN is
 * required and who is liable for GST; the app only explains the choice.
 */
enum class TaxCategoryOption(val wire: String, val title: String, val detail: String, val specifiedPremises: Boolean) {
    RESTAURANT_STANDALONE("RESTAURANT_STANDALONE", "Restaurant", "A restaurant, café or eatery", false),
    RESTAURANT_SPECIFIED_PREMISES(
        "RESTAURANT_SPECIFIED_PREMISES",
        "Restaurant in a hotel",
        "Inside a hotel whose room tariff makes it a specified premises",
        true,
    ),
    CLOUD_KITCHEN_TAKEAWAY("CLOUD_KITCHEN_TAKEAWAY", "Cloud kitchen or takeaway", "Delivery and takeaway only", false),
    OUTDOOR_CATERING("OUTDOOR_CATERING", "Outdoor catering", "Catering at your customers' venues", false),
    OUTDOOR_CATERING_SPECIFIED_PREMISES(
        "OUTDOOR_CATERING_SPECIFIED_PREMISES",
        "Outdoor catering at specified premises",
        "Catering at a hotel whose tariff makes it a specified premises",
        true,
    ),
    ;

    companion object {
        fun fromWire(wire: String?): TaxCategoryOption? = entries.firstOrNull { it.wire == wire }
    }
}

/** The compliance form's client checks, mirroring onboarding.ValidateCompliance. Keys are request fields. */
object ComplianceRules {
    const val FIELD_CATEGORY = "tax_category"
    const val FIELD_LEGAL_NAME = "legal_name"
    const val FIELD_PAN = "pan"
    const val FIELD_GSTIN = "gstin"
    const val FIELD_DECLARED_AT = "specified_premises_declared_at"
    private const val MAX_LEGAL_NAME = 200

    fun validate(
        category: TaxCategoryOption?,
        legalName: String,
        pan: String,
        gstin: String,
        declaredAt: String,
        today: LocalDate,
    ): Map<String, String> = buildMap {
        if (category == null) put(FIELD_CATEGORY, "Choose the category that fits your kitchen")
        val name = legalName.trim()
        if (name.isEmpty() || name.length > MAX_LEGAL_NAME) {
            put(FIELD_LEGAL_NAME, "Enter the legal business name, up to 200 characters")
        }
        val panCheck = Pan.check(pan)
        if (panCheck is PanCheck.Invalid) put(FIELD_PAN, panCheck.message)
        if (gstin.isNotBlank()) {
            when (val gstinCheck = Gstin.check(gstin)) {
                is GstinCheck.Invalid -> put(FIELD_GSTIN, gstinCheck.message)
                is GstinCheck.Valid -> if (panCheck is PanCheck.Valid && gstinCheck.pan != panCheck.normalized) {
                    put(FIELD_GSTIN, "The PAN inside this GSTIN doesn't match the PAN above")
                }
            }
        }
        if (category?.specifiedPremises == true) {
            val date = runCatching { LocalDate.parse(declaredAt.trim()) }.getOrNull()
            when {
                date == null -> put(FIELD_DECLARED_AT, "Enter the date the premises were declared")
                date.isAfter(today) -> put(FIELD_DECLARED_AT, "This date can't be in the future")
            }
        }
    }
}

data class ComplianceUiState(
    val category: TaxCategoryOption? = null,
    val legalName: String = "",
    val pan: String = "",
    val gstin: String = "",
    val declaredAt: String = "",
    val errors: Map<String, String> = emptyMap(),
    val saving: Boolean = false,
    val saved: ComplianceDto? = null,
    val message: UsMessage? = null,
)

/**
 * Tax category, legal name, PAN and GSTIN → `PUT …/compliance`.
 *
 * The PAN is cleared from memory as soon as it is saved: the server stores it
 * sealed and shows back only `pan_masked`. ROUTE GAP: there is no read, so a
 * returning partner sees an empty form.
 */
@HiltViewModel
class ComplianceViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val food: FoodRepository,
    private val clock: KitchenClock,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val _state = MutableStateFlow(ComplianceUiState())
    val state: StateFlow<ComplianceUiState> = _state.asStateFlow()

    val today: LocalDate get() = IndiaTime.today(clock)

    fun onCategory(option: TaxCategoryOption) = edit(ComplianceRules.FIELD_CATEGORY) { it.copy(category = option) }

    fun onLegalName(value: String) = edit(ComplianceRules.FIELD_LEGAL_NAME) { it.copy(legalName = value) }

    fun onPan(value: String) = edit(ComplianceRules.FIELD_PAN) { it.copy(pan = value.take(MAX_ID_INPUT)) }

    fun onGstin(value: String) = edit(ComplianceRules.FIELD_GSTIN) { it.copy(gstin = value.take(MAX_ID_INPUT)) }

    fun onDeclaredAt(value: String) = edit(ComplianceRules.FIELD_DECLARED_AT) { it.copy(declaredAt = value) }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun save() {
        val s = _state.value
        val errors = ComplianceRules.validate(s.category, s.legalName, s.pan, s.gstin, s.declaredAt, today)
        val category = s.category
        val pan = Pan.check(s.pan) as? PanCheck.Valid
        if (errors.isNotEmpty() || category == null || pan == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        val request = ComplianceRequest(
            taxCategory = category.wire,
            legalName = s.legalName.trim(),
            pan = pan.normalized,
            gstin = (Gstin.check(s.gstin) as? GstinCheck.Valid)?.normalized,
            specifiedPremisesDeclaredAt = s.declaredAt.trim().takeIf { category.specifiedPremises },
        )
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            when (val result = food.putCompliance(restaurantId, request)) {
                is FoodResult.Success -> _state.update {
                    it.copy(saving = false, saved = result.value, pan = "", message = success("Tax details saved"))
                }
                is FoodResult.Failure -> _state.update { current ->
                    val error = result.error
                    val field = (error as? FoodError.InvalidField)?.field
                    if (error is FoodError.InvalidField && field != null) {
                        current.copy(saving = false, errors = mapOf(field to error.message))
                    } else {
                        current.copy(saving = false, message = error.asMessage())
                    }
                }
            }
        }
    }

    private inline fun edit(field: String, crossinline change: (ComplianceUiState) -> ComplianceUiState) {
        _state.update { change(it).copy(errors = it.errors - field) }
    }

    private companion object {
        const val MAX_ID_INPUT = 20
    }
}
