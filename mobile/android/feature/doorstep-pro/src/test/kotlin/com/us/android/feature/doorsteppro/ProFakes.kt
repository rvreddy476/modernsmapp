package com.us.android.feature.doorsteppro

import com.us.android.feature.doorsteppro.data.AddressDto
import com.us.android.feature.doorsteppro.data.DayOffDto
import com.us.android.feature.doorsteppro.data.DigiLockerStartDto
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.DutyStateDto
import com.us.android.feature.doorsteppro.data.EarningsDto
import com.us.android.feature.doorsteppro.data.ExtraDto
import com.us.android.feature.doorsteppro.data.ExtraOptionDto
import com.us.android.feature.doorsteppro.data.ExtraRequest
import com.us.android.feature.doorsteppro.data.HoursWindowDto
import com.us.android.feature.doorsteppro.data.IncidentDto
import com.us.android.feature.doorsteppro.data.KycCheckDto
import com.us.android.feature.doorsteppro.data.LocationRequest
import com.us.android.feature.doorsteppro.data.MessageDto
import com.us.android.feature.doorsteppro.data.MessagePageDto
import com.us.android.feature.doorsteppro.data.OfferDto
import com.us.android.feature.doorsteppro.data.PayoutAccountDto
import com.us.android.feature.doorsteppro.data.PhotoDto
import com.us.android.feature.doorsteppro.data.PhotosRequiredDto
import com.us.android.feature.doorsteppro.data.ProAreaDto
import com.us.android.feature.doorsteppro.data.ProDocumentDto
import com.us.android.feature.doorsteppro.data.ProError
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.data.ProJobPageDto
import com.us.android.feature.doorsteppro.data.ProReadinessDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.ProSkillDto
import com.us.android.feature.doorsteppro.data.ProfessionalDto
import com.us.android.feature.doorsteppro.data.RatingDto
import com.us.android.feature.doorsteppro.data.RealtimeTokenDto
import com.us.android.feature.doorsteppro.data.ServiceabilityDto
import com.us.android.feature.doorsteppro.data.SkillDto
import com.us.android.feature.doorsteppro.data.SosRequest
import com.us.android.feature.doorsteppro.data.WeeklyHoursDto
import com.us.android.feature.doorsteppro.location.Coordinates
import com.us.android.feature.doorsteppro.location.CurrentLocationSource
import com.us.android.feature.doorsteppro.store.VisitMemory
import com.us.android.feature.doorsteppro.upload.PhotoUploads
import com.us.android.feature.doorsteppro.upload.UploadOutcome
import java.time.Instant

/** Test builders for the professional's DTOs. */
object Jobs {
    fun job(
        bookingId: String = "b-1",
        status: String = "assigned",
        slotStart: String = "2026-10-04T05:00:00Z",
        slotEnd: String = "2026-10-04T06:30:00Z",
        photos: PhotosRequiredDto = PhotosRequiredDto(before = 2, after = 2, kitSeal = 0),
        withAddress: Boolean = true,
    ) = ProJobDto(
        bookingId = bookingId,
        status = status,
        serviceName = "Kitchen deep cleaning",
        categorySlug = "home-cleaning",
        slotStart = slotStart,
        slotEnd = slotEnd,
        items = emptyList(),
        locality = "Kondapur",
        address = if (withAddress) address() else null,
        customerFirstName = "Asha",
        chatOpen = true,
        photosRequired = photos,
        earningEstimatePaise = 150_000,
    )

    fun address() = AddressDto(
        id = "a-1", label = "Home", line1 = "Flat 402, Lake View", line2 = null, landmark = null, locality = "Kondapur",
        cityCode = "HYD", pincode = "500084", lat = 17.46, lng = 78.36, zoneId = "z-1", isDefault = true,
        createdAt = "2026-10-01T00:00:00Z",
    )
}

/**
 * A scriptable repository: every route answers [notScripted] unless a test
 * sets its answer, and every call is recorded by name.
 */
@Suppress("TooManyFunctions")
class FakeProRepository : DoorstepProRepository {
    override suspend fun prices(): ProResult<List<com.us.android.feature.doorsteppro.data.ProServicePricingDto>> = notScripted
    override suspend fun submitPrice(request: com.us.android.feature.doorsteppro.data.ProPriceRequest): ProResult<com.us.android.feature.doorsteppro.data.ProPriceDto> = notScripted
    override suspend fun withdrawPrice(id: String): ProResult<com.us.android.feature.doorsteppro.data.ProPriceDto> = notScripted
    override suspend fun sameDay(serviceId: String, enabled: Boolean): ProResult<com.us.android.feature.doorsteppro.data.SameDaySettingDto> = notScripted
    val calls = mutableListOf<String>()
    private val notScripted: ProResult<Nothing> = ProResult.Failure(ProError.NotFound)

    var jobResult: ProResult<ProJobDto> = notScripted
    var startResult: ProResult<ProJobDto> = notScripted
    var completeResult: ProResult<ProJobDto> = notScripted
    var arrivedResult: ProResult<ProJobDto> = notScripted
    var photoResult: ProResult<PhotoDto> = ProResult.Success(PhotoDto("p-1", "b-1", "before", "m-1", "2026-10-04T05:00:00Z"))
    var otpsSent = mutableListOf<String>()
    var photosSent = mutableListOf<String>()

    override suspend fun apply(displayName: String, cityCode: String): ProResult<ProfessionalDto> = notScripted.also { calls += "apply" }
    override suspend fun me(): ProResult<ProfessionalDto> = notScripted.also { calls += "me" }
    override suspend fun updateProfile(displayName: String?, photoMediaId: String?): ProResult<ProfessionalDto> = notScripted
    override suspend fun readiness(): ProResult<ProReadinessDto> = notScripted
    override suspend fun startDigiLocker(): ProResult<DigiLockerStartDto> = notScripted
    override suspend fun completeDigiLocker(code: String, state: String): ProResult<ProReadinessDto> = notScripted
    override suspend fun submitSelfie(mediaId: String): ProResult<KycCheckDto> = notScripted
    override suspend fun skillCatalogue(): ProResult<List<SkillDto>> = notScripted
    override suspend fun declareSkills(codes: List<String>): ProResult<List<ProSkillDto>> = notScripted
    override suspend fun uploadTradeCertificate(skillCode: String, mediaId: String, issuedOn: String, number: String?): ProResult<ProDocumentDto> = notScripted
    override suspend fun saveArea(zoneIds: List<String>, homeLat: Double, homeLng: Double, radiusM: Int): ProResult<ProAreaDto> = notScripted
    override suspend fun hours(): ProResult<WeeklyHoursDto> = notScripted
    override suspend fun saveHours(windows: List<HoursWindowDto>): ProResult<WeeklyHoursDto> = notScripted
    override suspend fun daysOff(): ProResult<List<DayOffDto>> = notScripted
    override suspend fun addDayOff(date: String, reason: String?): ProResult<DayOffDto> = notScripted
    override suspend fun removeDayOff(date: String): ProResult<Unit> = notScripted
    override suspend fun saveBank(holder: String, accountNumber: String, ifsc: String): ProResult<PayoutAccountDto> = notScripted
    override suspend fun uploadPoliceCertificate(mediaId: String, issuedOn: String, number: String?): ProResult<ProDocumentDto> = notScripted
    override suspend fun acceptAgreement(version: String): ProResult<ProReadinessDto> = notScripted
    override suspend fun savePan(pan: String): ProResult<ProReadinessDto> = notScripted
    override suspend fun serviceability(lat: Double, lng: Double): ProResult<ServiceabilityDto> = notScripted
    override suspend fun dutyOn(location: LocationRequest): ProResult<DutyStateDto> = notScripted
    override suspend fun dutyOff(): ProResult<DutyStateDto> = notScripted
    override suspend fun postLocation(location: LocationRequest): ProResult<Unit> = notScripted
    override suspend fun offers(): ProResult<List<OfferDto>> = notScripted
    override suspend fun acceptOffer(offerId: String): ProResult<ProJobDto> = notScripted
    override suspend fun declineOffer(offerId: String, reason: String?): ProResult<Unit> = notScripted
    override suspend fun jobs(status: String?, cursor: String?): ProResult<ProJobPageDto> = notScripted
    override suspend fun job(bookingId: String): ProResult<ProJobDto> = jobResult.also { calls += "job" }
    override suspend fun realtimeToken(): ProResult<RealtimeTokenDto> = notScripted
    override suspend fun enRoute(bookingId: String): ProResult<ProJobDto> = notScripted
    override suspend fun arrived(bookingId: String, location: LocationRequest): ProResult<ProJobDto> = arrivedResult.also { calls += "arrived" }
    override suspend fun recordPhoto(bookingId: String, phase: String, mediaId: String, lat: Double?, lng: Double?): ProResult<PhotoDto> =
        photoResult.also { photosSent += phase }
    override suspend fun start(bookingId: String, otp: String): ProResult<ProJobDto> = startResult.also { otpsSent += "start:$otp" }
    override suspend fun extraOptions(bookingId: String): ProResult<List<ExtraOptionDto>> = notScripted
    override suspend fun extras(bookingId: String): ProResult<List<ExtraDto>> = notScripted
    override suspend fun proposeExtra(bookingId: String, request: ExtraRequest): ProResult<ExtraDto> = notScripted
    override suspend fun withdrawExtra(bookingId: String, extraId: String): ProResult<Unit> = notScripted
    override suspend fun finish(bookingId: String): ProResult<ProJobDto> = notScripted
    override suspend fun complete(bookingId: String, otp: String): ProResult<ProJobDto> = completeResult.also { otpsSent += "end:$otp" }
    override suspend fun customerNoShow(bookingId: String): ProResult<ProJobDto> = notScripted
    override suspend fun cancel(bookingId: String, reason: String): ProResult<Unit> = notScripted
    override suspend fun sos(bookingId: String, request: SosRequest): ProResult<IncidentDto> = notScripted
    override suspend fun unsafeExit(bookingId: String, request: SosRequest): ProResult<IncidentDto> = notScripted
    override suspend fun rateCustomer(bookingId: String, stars: Int, tags: List<String>, comment: String?): ProResult<RatingDto> = notScripted
    override suspend fun messages(bookingId: String, cursor: String?): ProResult<MessagePageDto> = notScripted
    override suspend fun sendMessage(bookingId: String, body: String): ProResult<MessageDto> = notScripted
    override suspend fun readMessage(bookingId: String, messageId: String): ProResult<Unit> = ProResult.Success(Unit)
    override suspend fun earnings(from: String?, to: String?): ProResult<EarningsDto> = notScripted
}

class FakeLocation(var fix: Coordinates? = Coordinates(17.46, 78.36), var granted: Boolean = true) : CurrentLocationSource {
    override fun hasPermission(): Boolean = granted
    override suspend fun current(): Coordinates? = fix
}

class FakeUploads(var outcome: UploadOutcome = UploadOutcome.Ready("m-1")) : PhotoUploads {
    override suspend fun uploadImage(uri: String, onProgress: (Float) -> Unit): UploadOutcome = outcome
}

class FakeVisitMemory : VisitMemory {
    val arrived = mutableMapOf<String, Instant>()
    val finishedIds = mutableSetOf<String>()
    override fun arrivedAt(bookingId: String): Instant? = arrived[bookingId]
    override fun markArrived(bookingId: String, at: Instant) {
        arrived[bookingId] = at
    }
    override fun finished(bookingId: String): Boolean = bookingId in finishedIds
    override fun markFinished(bookingId: String) {
        finishedIds += bookingId
    }
}
