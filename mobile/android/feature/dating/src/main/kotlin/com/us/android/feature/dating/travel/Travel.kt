package com.us.android.feature.dating.travel

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.dating.DatingCopy
import com.us.android.feature.dating.DatingSession
import com.us.android.feature.dating.DeviceZone
import com.us.android.feature.dating.data.DatingError
import com.us.android.feature.dating.data.DatingRepository
import com.us.android.feature.dating.data.DatingResult
import com.us.android.feature.dating.data.code
import com.us.android.feature.dating.data.detailsAs
import com.us.android.feature.dating.home.parseInstant
import com.us.android.feature.dating.network.AllowedDetailsDto
import com.us.android.feature.dating.network.RangeDetailsDto
import com.us.android.feature.dating.network.TravelCityDto
import com.us.android.feature.dating.network.TravelDto
import com.us.android.feature.dating.network.TravelTripDto
import com.us.android.feature.dating.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale
import javax.inject.Inject

/** A travel destination, as the server lists it. */
data class TravelCityUi(val code: String, val label: String)

/** The trip in effect. A time the server sent that does not parse is null, and nothing is drawn for it. */
data class TripUi(
    val cityCode: String,
    val cityLabel: String,
    val startsAt: Instant?,
    val endsAt: Instant?,
)

enum class TravelPhase { LOADING, READY, FAILED, OFF }

/**
 * The Travel screen (mechanic M8).
 *
 * [available] is the server's word that the viewer holds a pass. Without one
 * the screen is LOCKED: the cities still show, Start asks for Premium instead
 * of the server, and a `403 TRAVEL_REQUIRES_PASS` locks it the same way.
 */
data class TravelUiState(
    val phase: TravelPhase = TravelPhase.LOADING,
    val available: Boolean = false,
    /** Alphabetical by label. */
    val cities: List<TravelCityUi> = emptyList(),
    val maxDays: Int = TravelRules.DEFAULT_MAX_DAYS,
    val trip: TripUi? = null,
    /** The city code picked for the next trip. */
    val city: String? = null,
    val days: Int = TravelRules.DEFAULT_DAYS,
    val saving: Boolean = false,
    val ending: Boolean = false,
    val cityError: String? = null,
    val daysError: String? = null,
    /** The Premium upsell is open. */
    val upsell: Boolean = false,
    val message: UsMessage? = null,
) {
    val locked: Boolean get() = !available
    val busy: Boolean get() = saving || ending
    val canStart: Boolean get() = available && city != null && !busy
}

/** Travel's pure rules. */
object TravelRules {

    /** The server's longest trip, used when it sends none. */
    const val DEFAULT_MAX_DAYS = 7
    const val DEFAULT_DAYS = 3
    const val MIN_DAYS = 1

    /** The mechanic name the session switches off on `MECHANIC_NOT_ENABLED`. */
    const val MECHANIC = "travel"

    /** The cities as the picker lists them: alphabetical by label, each code once, blanks dropped. */
    fun cities(dto: List<TravelCityDto>): List<TravelCityUi> =
        dto.mapNotNull { city ->
            val code = city.code.trim().takeIf { it.isNotEmpty() } ?: return@mapNotNull null
            TravelCityUi(code, city.label.trim().ifEmpty { code })
        }
            .distinctBy { it.code }
            .sortedWith(compareBy(String.CASE_INSENSITIVE_ORDER) { it.label })

    fun maxDays(dto: TravelDto): Int = dto.maxDays.takeIf { it > 0 } ?: DEFAULT_MAX_DAYS

    fun clampDays(days: Int, max: Int): Int = days.coerceIn(MIN_DAYS, max.coerceAtLeast(MIN_DAYS))

    /** The trip, or null when none is on (absent, or a city with neither code nor label). */
    fun trip(dto: TravelTripDto?): TripUi? {
        val trip = dto ?: return null
        val code = trip.city.code.trim()
        val label = trip.city.label.trim().ifEmpty { code }
        if (label.isEmpty()) return null
        return TripUi(code, label, parseInstant(trip.startsAt), parseInstant(trip.endsAt))
    }
}

/** Travel's words. Our own. */
object TravelCopy {
    const val TITLE = "Travel"
    const val ENTRY = "Travel"
    const val WHERE = "Where to"
    const val HOW_LONG = "How long"
    const val START = "Start trip"
    const val END = "End trip"
    const val PICK_CITY = "Pick a city first."
    const val CITY_GONE = "That city isn't on the list any more. Pick another one."
    const val LOCKED_TITLE = "Travel comes with Premium"
    const val LOCKED_BODY =
        "With a Premium pass you can browse another city before you get there. People there see you as visiting, never where you are now."
    const val SEE_PREMIUM = "See Premium"
    const val NOT_NOW = "Not now"
    const val UPSELL_TITLE = "Travel with Premium"
    const val UPSELL_BODY = "A Premium pass lets you set Pulse and your picks to another city for a few days."
    const val INTRO =
        "Set Pulse and your picks to another city for a few days. People there see you as visiting. Your real location is never shown."
    const val OFF_TITLE = "Travel isn't available right now"
    const val OFF_BODY = "Check back later."
    const val LOAD_FAILED = "Travel didn't load"
    const val ENDED = "Trip ended. Pulse is back to your own area."

    fun days(n: Int): String = if (n == 1) "1 day" else "$n days"

    fun daysRange(min: Int, max: Int): String = "Trips run from $min to ${days(max)}."

    /** The marker on someone else's card while they are on a trip: "Visiting Hyderabad". */
    fun visiting(city: String?): String = city?.trim()?.takeIf { it.isNotEmpty() }?.let { "Visiting $it" } ?: "Visiting"

    /** The banner on the deck: "Browsing Mumbai until 9 Oct". */
    fun browsingUntil(trip: TripUi, zone: ZoneId): String {
        val until = trip.endsAt?.let { DAY.format(it.atZone(zone)) }
        return if (until != null) "Browsing ${trip.cityLabel} until $until" else "Browsing ${trip.cityLabel}"
    }

    fun started(trip: TripUi, zone: ZoneId): String = "You're set. ${browsingUntil(trip, zone)}."

    private val DAY = DateTimeFormatter.ofPattern("d MMM", Locale.ENGLISH)
}

/**
 * The travelling marker for someone else's card (mechanic M8), or null when
 * they are at home. [city] is already the destination: the server sends it so.
 */
fun visitingLabel(travelling: Boolean, city: String?): String? = if (travelling) TravelCopy.visiting(city) else null

/**
 * Travel mode: pick a city and 1 to `max_days` days, start the trip, end it.
 *
 * A start or an end tells the session ([DatingSession.travelChanged]), so the
 * deck and the picks read afresh — while a trip is on they are the
 * destination's. A refusal names what to fix: the city (`INVALID_CITY`, whose
 * allowed list narrows the picker), the days (`INVALID_TRAVEL_DAYS`, whose
 * bounds become the stepper's), or the pass (`TRAVEL_REQUIRES_PASS`, which
 * locks the screen). `MECHANIC_NOT_ENABLED` switches travel off for the session.
 */
@Suppress("TooManyFunctions")
@HiltViewModel
class TravelViewModel @Inject constructor(
    private val repository: DatingRepository,
    private val session: DatingSession,
    private val zone: DeviceZone,
) : ViewModel() {

    private val _state = MutableStateFlow(TravelUiState())
    val state: StateFlow<TravelUiState> = _state.asStateFlow()

    init {
        refresh()
    }

    /** Reads the trip again: on open, and back from Premium, where a pass may have landed. A draft is kept. */
    fun refresh() {
        if (session.isMechanicDisabled(TravelRules.MECHANIC)) {
            _state.update { it.copy(phase = TravelPhase.OFF) }
            return
        }
        viewModelScope.launch {
            when (val result = repository.travel()) {
                is DatingResult.Success -> apply(result.value)
                is DatingResult.Failure -> when {
                    isOff(result.error) -> switchOff()
                    // A failed re-read keeps the screen that is already there.
                    _state.value.phase == TravelPhase.READY -> _state.update { it.copy(message = DatingCopy.message(result.error)) }
                    else -> _state.update { it.copy(phase = TravelPhase.FAILED) }
                }
            }
        }
    }

    fun selectCity(code: String?) {
        if (_state.value.busy) return
        _state.update { it.copy(city = code?.takeIf { c -> it.cities.any { city -> city.code == c } }, cityError = null) }
    }

    fun moreDays() = setDays(_state.value.days + 1)

    fun fewerDays() = setDays(_state.value.days - 1)

    fun setDays(days: Int) {
        if (_state.value.busy) return
        _state.update { it.copy(days = TravelRules.clampDays(days, it.maxDays), daysError = null) }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun showUpsell() = _state.update { it.copy(upsell = true) }

    fun dismissUpsell() = _state.update { it.copy(upsell = false) }

    /** Starts the trip. Without a pass the server is not asked: the upsell opens instead. */
    fun start() {
        val current = _state.value
        if (current.phase != TravelPhase.READY || current.busy) return
        if (current.locked) {
            _state.update { it.copy(upsell = true) }
            return
        }
        val city = current.city
        if (city == null) {
            _state.update { it.copy(cityError = TravelCopy.PICK_CITY) }
            return
        }
        if (current.days !in TravelRules.MIN_DAYS..current.maxDays) {
            _state.update { it.copy(daysError = TravelCopy.daysRange(TravelRules.MIN_DAYS, it.maxDays)) }
            return
        }
        _state.update { it.copy(saving = true, cityError = null, daysError = null) }
        viewModelScope.launch {
            when (val result = repository.startTravel(city, current.days)) {
                is DatingResult.Success -> {
                    apply(result.value)
                    session.travelChanged()
                    val trip = _state.value.trip
                    _state.update { it.copy(message = trip?.let { t -> successMessage(TravelCopy.started(t, zone.zone())) }) }
                }
                is DatingResult.Failure -> startRefused(result.error)
            }
        }
    }

    /** Ends the trip in effect. */
    fun end() {
        val current = _state.value
        if (current.trip == null || current.busy) return
        _state.update { it.copy(ending = true) }
        viewModelScope.launch {
            when (val result = repository.endTravel()) {
                is DatingResult.Success -> {
                    apply(result.value)
                    session.travelChanged()
                    _state.update { it.copy(message = successMessage(TravelCopy.ENDED)) }
                }
                is DatingResult.Failure -> {
                    if (isOff(result.error)) {
                        switchOff()
                    } else {
                        _state.update { it.copy(ending = false, message = DatingCopy.message(result.error, repository.json)) }
                    }
                }
            }
        }
    }

    private fun startRefused(error: DatingError) {
        when (error.code) {
            CODE_REQUIRES_PASS -> _state.update { it.copy(saving = false, available = false, upsell = true) }
            CODE_INVALID_CITY -> {
                val allowed = error.detailsAs(repository.json, AllowedDetailsDto.serializer())?.allowed.orEmpty().toSet()
                _state.update { s ->
                    s.copy(
                        saving = false,
                        // The server's list wins: a city it no longer has leaves the picker.
                        cities = if (allowed.isEmpty()) s.cities else s.cities.filter { it.code in allowed },
                        city = null,
                        cityError = TravelCopy.CITY_GONE,
                    )
                }
            }
            CODE_INVALID_DAYS -> {
                val range = error.detailsAs(repository.json, RangeDetailsDto.serializer())
                _state.update { s ->
                    val max = range?.max?.takeIf { it > 0 } ?: s.maxDays
                    val min = range?.min?.takeIf { it > 0 } ?: TravelRules.MIN_DAYS
                    s.copy(
                        saving = false,
                        maxDays = max,
                        days = TravelRules.clampDays(s.days, max),
                        daysError = TravelCopy.daysRange(min, max),
                    )
                }
            }
            CODE_NOT_ENABLED -> switchOff()
            else -> _state.update { it.copy(saving = false, message = DatingCopy.message(error, repository.json)) }
        }
    }

    /** The server's answer onto the screen, keeping the draft where it still fits. */
    private fun apply(dto: TravelDto) {
        val cities = TravelRules.cities(dto.cities)
        val max = TravelRules.maxDays(dto)
        _state.update { s ->
            s.copy(
                phase = TravelPhase.READY,
                available = dto.available,
                cities = cities,
                maxDays = max,
                trip = TravelRules.trip(dto.active),
                city = s.city?.takeIf { code -> cities.any { it.code == code } },
                days = TravelRules.clampDays(s.days, max),
                saving = false,
                ending = false,
                // A pass that has landed closes the upsell.
                upsell = s.upsell && !dto.available,
            )
        }
    }

    private fun switchOff() {
        session.disableMechanic(TravelRules.MECHANIC)
        _state.update { it.copy(phase = TravelPhase.OFF, saving = false, ending = false) }
    }

    private fun isOff(error: DatingError): Boolean = error.code == CODE_NOT_ENABLED

    private companion object {
        const val CODE_REQUIRES_PASS = "TRAVEL_REQUIRES_PASS"
        const val CODE_INVALID_CITY = "INVALID_CITY"
        const val CODE_INVALID_DAYS = "INVALID_TRAVEL_DAYS"
        const val CODE_NOT_ENABLED = "MECHANIC_NOT_ENABLED"
    }
}
