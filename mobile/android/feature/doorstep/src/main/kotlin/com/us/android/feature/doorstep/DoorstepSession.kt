package com.us.android.feature.doorstep

import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.ServiceDetailDto
import com.us.android.feature.doorstep.domain.ServiceSelection
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import javax.inject.Inject
import javax.inject.Singleton

/** A service page's pick, carried to the slot step. */
data class ServiceDraft(val service: ServiceDetailDto, val selection: ServiceSelection)

/**
 * What Doorstep's screens share for the life of the process: the city, the
 * address the customer picked, and the service draft between the service page
 * and the slot step.
 *
 * Nothing money-relevant lives here. Checkout keeps its own saved state and
 * reads the quote back from the server, so losing this to process death only
 * means picking the service again before a slot — never a second booking.
 */
@Singleton
class DoorstepSession @Inject constructor() {

    private val _city = MutableStateFlow(PILOT_CITY)

    /** The city the catalogue is read for. Hyderabad until a second city opens. */
    val city: StateFlow<String> = _city.asStateFlow()

    private val _address = MutableStateFlow<AddressDto?>(null)
    val address: StateFlow<AddressDto?> = _address.asStateFlow()

    private val _draft = MutableStateFlow<ServiceDraft?>(null)
    val draft: StateFlow<ServiceDraft?> = _draft.asStateFlow()

    fun selectAddress(address: AddressDto?) {
        _address.value = address
        address?.cityCode?.takeIf { it.isNotBlank() }?.let { _city.value = it }
    }

    /** Keeps the picked address if it still exists, else the default, else the first. */
    fun reconcileAddresses(addresses: List<AddressDto>) {
        _address.update { current ->
            addresses.firstOrNull { it.id == current?.id }
                ?: addresses.firstOrNull { it.isDefault }
                ?: addresses.firstOrNull()
        }
    }

    fun setDraft(draft: ServiceDraft?) {
        _draft.value = draft
    }

    companion object {
        /** The pilot city (GST state 36). */
        const val PILOT_CITY = "HYD"
    }
}
